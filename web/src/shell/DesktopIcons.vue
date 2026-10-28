<script setup lang="ts">
// Desktop icons: Computer, Home and Trash, then the contents of the user's
// Desktop folder (from ~/.config/user-dirs.dirs). Icons sit on a grid; they
// keep the cells the user dragged them to (saved by the worker, first taken
// from Nemo's layout) and the rest fill columns top to bottom, like Nemo's
// desktop (D36).
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from 'reka-ui'
import { fsApi, joinPath, uniqueName } from '@/api/fs'
import type { Cell, Entry } from '@/api/types'
import FileIcon from '@/components/FileIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { emitFsChanged, onFsChanged } from '@/lib/bus'
import { useWatchedDirs } from '@/lib/fswatch'
import { dragPaths, dropOp, isFolderDrag, isNoopDrop, isPathDrag, setDragPaths, setDropEffect } from '@/lib/dnd'
import { deletePermanently, moveToTrash, paste, startJob, type FileOpsHost } from '@/lib/fileops'
import { openEntry } from '@/shell/actions'
import { cellsFromNemo, moveIcons, placeIcons, positionsToSave, type Grid } from '@/shell/desktopLayout'
import { useClipboard } from '@/stores/clipboard'
import { useDialogs } from '@/stores/dialogs'
import { useNotices } from '@/stores/notices'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'

interface Item {
  key: string
  /** Key in the saved layout: the file name, or a URI for the built-in icons. */
  layoutKey: string
  name: string
  icon: string[]
  entry?: Entry
  open: () => void
}

const session = useSession()
const wm = useWindows()
const notices = useNotices()
const dialogs = useDialogs()
const clipboard = useClipboard()
const host: FileOpsHost = { confirm: dialogs.confirm, notify: notices.push, clipboard }

const desktopDir = ref('')
const trashFilesDir = ref('')
/** files/ folders of all trashes (home and drives), for the full/empty icon. */
const trashDirs = ref<string[]>([])
const entries = ref<Entry[]>([])
const trashFull = ref(false)
const selected = ref(new Set<string>())
const menuTarget = ref<Item | null>(null)
const dropActive = ref(false)

const home = computed(() => session.info?.home ?? '/')

const items = computed<Item[]>(() => [
  {
    key: 'computer',
    layoutKey: 'computer:///',
    name: 'Computer',
    icon: ['computer'],
    open: () => wm.open('files', { path: 'computer://' }),
  },
  {
    key: 'home',
    layoutKey: 'home:///',
    name: 'Home',
    icon: ['user-home'],
    open: () => wm.open('files', { path: home.value }),
  },
  {
    key: 'trash',
    layoutKey: 'trash:///',
    name: 'Trash',
    icon: [trashFull.value ? 'user-trash-full' : 'user-trash'],
    open: () => wm.open('files', { path: 'trash://' }),
  },
  ...entries.value
    .filter((e) => !e.hidden)
    .map((e) => ({
      key: e.path,
      layoutKey: e.name,
      name: e.name,
      icon: iconsForEntry(e),
      entry: e,
      open: () => openEntry(e),
    })),
])

// Grid geometry: the cell size comes from the theme, padding and gap match
// the stylesheet below.
const PAD = 8
const GAP = 4
const root = ref<HTMLElement | null>(null)
const cellW = ref(96)
const cellH = ref(92)
const grid = ref<Grid>({ cols: 1, rows: 1 })

function measure() {
  const el = root.value
  if (!el) return
  const style = getComputedStyle(el)
  cellW.value = parseFloat(style.getPropertyValue('--desktop-cell-w')) || cellW.value
  cellH.value = parseFloat(style.getPropertyValue('--desktop-cell-h')) || cellH.value
  grid.value = {
    cols: Math.max(1, Math.floor((el.clientWidth - 2 * PAD + GAP) / (cellW.value + GAP))),
    rows: Math.max(1, Math.floor((el.clientHeight - 2 * PAD + GAP) / (cellH.value + GAP))),
  }
}

/** Saved cells, or Nemo's arrangement until the user arranges icons in Tilde. */
const wanted = ref<Record<string, Cell>>({})
const placement = computed(() =>
  placeIcons(
    items.value.map((i) => i.layoutKey),
    wanted.value,
    grid.value,
  ),
)

function iconStyle(item: Item) {
  const c = placement.value.get(item.layoutKey) ?? { col: 0, row: 0 }
  return { left: `${PAD + c.col * (cellW.value + GAP)}px`, top: `${PAD + c.row * (cellH.value + GAP)}px` }
}

async function loadLayout() {
  try {
    const l = await fsApi.desktopLayout()
    wanted.value = l.saved ? l.positions : cellsFromNemo(l.nemo)
  } catch {
    // Without a layout the icons simply fill the grid.
  }
}

/** Saves where every icon is now; files no longer on the desktop are forgotten. */
async function saveLayout(next: Map<string, Cell>, moved: string[]) {
  const keep = positionsToSave(next, wanted.value, grid.value, moved)
  const positions: Record<string, Cell | null> = { ...keep }
  for (const k of Object.keys(wanted.value)) if (!(k in keep)) positions[k] = null
  wanted.value = keep
  try {
    wanted.value = (await fsApi.saveDesktopLayout(positions)).positions
  } catch (e) {
    notices.push(`Could not save the icon layout: ${(e as Error).message}`, 'error')
  }
}

async function organizeByName() {
  wanted.value = {}
  try {
    await fsApi.saveDesktopLayout({}, true)
  } catch (e) {
    notices.push(`Could not save the icon layout: ${(e as Error).message}`, 'error')
  }
}

/** A drag of desktop icons within the desktop: what moves, and where the pointer held the icon. */
interface IconDrag {
  keys: string[]
  anchor: string
  dx: number
  dy: number
}
let iconDrag: IconDrag | null = null

let loading = false
let reloadQueued = false

/** Reloads the icons; a request while one runs queues a single rerun. */
async function load() {
  if (loading) {
    reloadQueued = true
    return
  }
  loading = true
  try {
    if (!desktopDir.value) {
      const places = await fsApi.places()
      desktopDir.value = places.find((p) => p.id === 'desktop')?.path ?? joinPath(home.value, 'Desktop')
      trashFilesDir.value = places.find((p) => p.id === 'trash')?.path ?? ''
    }
    const [list, trash] = await Promise.allSettled([fsApi.list(desktopDir.value), fsApi.trashList()])
    entries.value = list.status === 'fulfilled' ? list.value.entries : []
    trashFull.value = trash.status === 'fulfilled' && trash.value.items.length > 0
    if (trash.status === 'fulfilled') trashDirs.value = trash.value.filesDirs
  } catch {
    // Session problems are handled globally; keep the last known icons.
  } finally {
    loading = false
    if (reloadQueued) {
      reloadQueued = false
      void load()
    }
  }
}

function select(item: Item, e: MouseEvent) {
  if (e.ctrlKey) {
    const s = new Set(selected.value)
    if (s.has(item.key)) s.delete(item.key)
    else s.add(item.key)
    selected.value = s
  } else {
    selected.value = new Set([item.key])
  }
}

function onItemContext(item: Item) {
  menuTarget.value = item
  if (!selected.value.has(item.key)) selected.value = new Set([item.key])
}

function onBackgroundContext(e: MouseEvent) {
  if (!(e.target as Element).closest('.desktop-icon')) {
    menuTarget.value = null
    selected.value = new Set()
  }
}

const selectedEntries = computed(() =>
  items.value.filter((i) => i.entry && selected.value.has(i.key)).map((i) => i.entry!),
)

const cutPaths = computed(() => new Set(clipboard.op === 'cut' ? clipboard.paths : []))

async function trashSelected() {
  await moveToTrash(
    host,
    selectedEntries.value.map((e) => e.path),
    trashFilesDir.value,
  )
}

async function deleteSelected() {
  await deletePermanently(
    host,
    selectedEntries.value.map((e) => e.path),
  )
}

function copySelected(op: 'copy' | 'cut') {
  const paths = selectedEntries.value.map((e) => e.path)
  if (paths.length) clipboard.set(op, paths)
}

async function newFolder() {
  const names = new Set(entries.value.map((e) => e.name))
  try {
    await fsApi.mkdir(joinPath(desktopDir.value, uniqueName('Untitled Folder', names)))
    emitFsChanged(desktopDir.value)
  } catch (e) {
    notices.push(`Could not create a folder: ${(e as Error).message}`, 'error')
  }
}

function onKey(e: KeyboardEvent) {
  const ctrl = e.ctrlKey || e.metaKey
  if (e.key === 'Delete' && e.shiftKey) void deleteSelected()
  else if (e.key === 'Delete') void trashSelected()
  else if (ctrl && e.key.toLowerCase() === 'c') copySelected('copy')
  else if (ctrl && e.key.toLowerCase() === 'x') copySelected('cut')
  else if (ctrl && e.key.toLowerCase() === 'v') void paste(host, desktopDir.value)
  if (e.key === 'Enter') items.value.filter((i) => selected.value.has(i.key)).forEach((i) => i.open())
}

const dropTarget = ref<string | null>(null)

/** Where a drop on this icon goes: a folder, Home, or the trash. */
function dropDest(item: Item): string | null {
  if (item.key === 'trash') return 'trash'
  if (item.key === 'home') return home.value
  return item.entry?.type === 'dir' ? item.entry.path : null
}

function onDragStart(item: Item, e: DragEvent) {
  if (!selected.value.has(item.key)) selected.value = new Set([item.key])
  const box = (e.currentTarget as HTMLElement).getBoundingClientRect()
  iconDrag = {
    keys: items.value.filter((i) => selected.value.has(i.key)).map((i) => i.layoutKey),
    anchor: item.layoutKey,
    dx: e.clientX - box.left,
    dy: e.clientY - box.top,
  }
  const paths = selectedEntries.value.map((x) => x.path)
  // Files can also be dropped on folders, the trash, other windows and
  // terminals; the built-in icons only move on the desktop.
  if (paths.length) setDragPaths(e, paths)
  else if (e.dataTransfer) {
    e.dataTransfer.setData('application/x-tilde-desktop-icons', iconDrag.keys.join('\n'))
    e.dataTransfer.effectAllowed = 'move'
  }
}

function onDragEnd() {
  iconDrag = null
}

function onIconDragOver(item: Item, e: DragEvent) {
  // Over one of the dragged icons, the drop is a move on the desktop.
  if (iconDrag?.keys.includes(item.layoutKey)) return
  if (!isPathDrag(e) || !dropDest(item)) return
  e.preventDefault()
  e.stopPropagation()
  setDropEffect(e)
  dropTarget.value = item.key
}

async function dropPathsOn(dest: string, e: DragEvent) {
  const paths = dragPaths(e)
  if (dest === 'trash') return moveToTrash(host, paths, trashFilesDir.value)
  if (!paths.length || isNoopDrop(paths, dest)) return
  await startJob(host, dropOp(e), paths, dest)
}

function onIconDrop(item: Item, e: DragEvent) {
  dropTarget.value = null
  const dest = dropDest(item)
  if (!isPathDrag(e) || !dest) return
  e.preventDefault()
  e.stopPropagation()
  void dropPathsOn(dest, e)
}

function onBackgroundDragOver(e: DragEvent) {
  if (iconDrag) {
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    return
  }
  // Not an editor tab or text: those are not for the desktop (D63).
  if (!isFolderDrag(e)) return
  e.preventDefault()
  if (isPathDrag(e)) setDropEffect(e)
  dropActive.value = true
}

/** Moves the dragged icons so the held one lands in the cell under its new position. */
function dropIcons(d: IconDrag, e: DragEvent) {
  const box = root.value!.getBoundingClientRect()
  const x = e.clientX - box.left - d.dx + cellW.value / 2
  const y = e.clientY - box.top - d.dy + cellH.value / 2
  const target = {
    col: Math.max(0, Math.floor((x - PAD) / (cellW.value + GAP))),
    row: Math.max(0, Math.floor((y - PAD) / (cellH.value + GAP))),
  }
  void saveLayout(moveIcons(placement.value, d.keys, d.anchor, target, grid.value), d.keys)
}

async function onDrop(e: DragEvent) {
  dropActive.value = false
  if (iconDrag) {
    const d = iconDrag
    iconDrag = null
    return dropIcons(d, e)
  }
  if (isPathDrag(e)) return dropPathsOn(desktopDir.value, e)
  const files = [...(e.dataTransfer?.files ?? [])]
  for (const f of files) {
    try {
      await fsApi.upload(joinPath(desktopDir.value, f.name), f)
    } catch (err) {
      notices.push(`Could not upload ${f.name}: ${(err as Error).message}`, 'error')
    }
  }
  if (files.length) emitFsChanged(desktopDir.value)
}

// The worker reports changes to these folders (and the trash, for the full
// or empty icon) over the events socket.
useWatchedDirs(() => [desktopDir.value, trashFilesDir.value, ...trashDirs.value])

let offFs: (() => void) | undefined
let ro: ResizeObserver | undefined
onMounted(() => {
  measure()
  ro = new ResizeObserver(measure)
  if (root.value) ro.observe(root.value)
  void loadLayout()
  void load()
  offFs = onFsChanged((dir) => {
    if (dir === desktopDir.value || dir === trashFilesDir.value || trashDirs.value.includes(dir)) void load()
  })
})
onBeforeUnmount(() => {
  offFs?.()
  ro?.disconnect()
})
</script>

<template>
  <ContextMenuRoot>
    <ContextMenuTrigger as-child>
      <div
        ref="root"
        class="desktop-icons"
        :class="{ drop: dropActive }"
        tabindex="0"
        @pointerdown.self="selected = new Set()"
        @contextmenu="onBackgroundContext"
        @keydown="onKey"
        @dragover="onBackgroundDragOver"
        @dragleave.self="dropActive = false"
        @drop.prevent="onDrop"
      >
        <button
          v-for="item in items"
          :key="item.key"
          class="desktop-icon"
          :class="{ selected: selected.has(item.key), cut: cutPaths.has(item.key), target: dropTarget === item.key }"
          :style="iconStyle(item)"
          draggable="true"
          :title="item.name"
          @click="select(item, $event)"
          @dblclick="item.open()"
          @contextmenu="onItemContext(item)"
          @dragstart="onDragStart(item, $event)"
          @dragend="onDragEnd"
          @dragover="onIconDragOver(item, $event)"
          @dragleave="dropTarget === item.key && (dropTarget = null)"
          @drop="onIconDrop(item, $event)"
        >
          <FileIcon :name="item.icon" :size="48" :entry="item.entry" />
          <span class="label">{{ item.name }}</span>
        </button>
      </div>
    </ContextMenuTrigger>
    <ContextMenuPortal>
      <ContextMenuContent class="popup-menu">
        <template v-if="menuTarget">
          <ContextMenuItem class="item" @select="menuTarget.open()">Open</ContextMenuItem>
          <template v-if="menuTarget.entry">
            <ContextMenuSeparator class="separator" />
            <ContextMenuItem class="item" @select="copySelected('cut')">Cut</ContextMenuItem>
            <ContextMenuItem class="item" @select="copySelected('copy')">Copy</ContextMenuItem>
            <ContextMenuSeparator class="separator" />
            <ContextMenuItem class="item" @select="trashSelected">Move to Trash</ContextMenuItem>
            <ContextMenuItem class="item" @select="deleteSelected">Delete Permanently…</ContextMenuItem>
            <ContextMenuSeparator class="separator" />
            <ContextMenuItem
              class="item"
              @select="wm.open('file-properties', { paths: selectedEntries.map((e) => e.path) })"
            >
              Properties
            </ContextMenuItem>
          </template>
        </template>
        <template v-else>
          <ContextMenuItem class="item" @select="newFolder">New Folder</ContextMenuItem>
          <ContextMenuItem class="item" :disabled="!clipboard.op" @select="paste(host, desktopDir)">
            Paste
          </ContextMenuItem>
          <ContextMenuItem class="item" @select="wm.open('terminal', { cwd: desktopDir })">
            Open in Terminal
          </ContextMenuItem>
          <ContextMenuSeparator class="separator" />
          <ContextMenuItem class="item" @select="organizeByName">Organize Desktop by Name</ContextMenuItem>
          <ContextMenuItem class="item" @select="wm.open('backgrounds')">Change Desktop Background</ContextMenuItem>
          <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
        </template>
      </ContextMenuContent>
    </ContextMenuPortal>
  </ContextMenuRoot>
</template>

<style scoped>
.desktop-icons {
  /* Icons are placed in script on a grid of --desktop-cell-w/h cells with
     PAD padding and GAP gaps (desktopLayout.ts). */
  position: absolute;
  inset: 0;
  overflow: hidden;
  outline: none;
}
.desktop-icons.drop {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.desktop-icon {
  position: absolute;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  width: var(--desktop-cell-w);
  height: var(--desktop-cell-h);
  padding: 6px 4px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--desktop-icon-fg);
  text-shadow: var(--desktop-icon-shadow);
  overflow: hidden;
}
.desktop-icon:hover {
  background: var(--desktop-icon-hover-bg);
}
.desktop-icon.selected {
  background: var(--desktop-icon-selected-bg);
}
.desktop-icon.target {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.desktop-icon.cut {
  opacity: 0.6;
}
.label {
  max-width: 100%;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  text-align: center;
  line-height: 1.2;
}
</style>
