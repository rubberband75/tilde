<script setup lang="ts">
// Desktop icons: Computer, Home and Trash, then the contents of the user's
// Desktop folder (from ~/.config/user-dirs.dirs). Icons fill columns top to
// bottom, like Nemo's desktop.
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
import type { Entry } from '@/api/types'
import FileIcon from '@/components/FileIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { emitFsChanged, onFsChanged } from '@/lib/bus'
import { useWatchedDirs } from '@/lib/fswatch'
import { dragPaths, dropOp, isNoopDrop, isPathDrag, setDragPaths, setDropEffect } from '@/lib/dnd'
import { deletePermanently, moveToTrash, paste, startJob, type FileOpsHost } from '@/lib/fileops'
import { openEntry } from '@/shell/actions'
import { useClipboard } from '@/stores/clipboard'
import { useDialogs } from '@/stores/dialogs'
import { useNotices } from '@/stores/notices'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'

interface Item {
  key: string
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
const entries = ref<Entry[]>([])
const trashFull = ref(false)
const selected = ref(new Set<string>())
const menuTarget = ref<Item | null>(null)
const dropActive = ref(false)

const home = computed(() => session.info?.home ?? '/')

const items = computed<Item[]>(() => [
  { key: 'computer', name: 'Computer', icon: ['computer'], open: () => wm.open('files', { path: 'computer://' }) },
  { key: 'home', name: 'Home', icon: ['user-home'], open: () => wm.open('files', { path: home.value }) },
  {
    key: 'trash',
    name: 'Trash',
    icon: [trashFull.value ? 'user-trash-full' : 'user-trash'],
    open: () => wm.open('files', { path: 'trash://' }),
  },
  ...entries.value
    .filter((e) => !e.hidden)
    .map((e) => ({ key: e.path, name: e.name, icon: iconsForEntry(e), entry: e, open: () => openEntry(e) })),
])

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
  if (!item.entry) return e.preventDefault()
  if (!selected.value.has(item.key)) selected.value = new Set([item.key])
  setDragPaths(
    e,
    selectedEntries.value.map((x) => x.path),
  )
}

function onIconDragOver(item: Item, e: DragEvent) {
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
  e.preventDefault()
  if (isPathDrag(e)) setDropEffect(e)
  dropActive.value = true
}

async function onDrop(e: DragEvent) {
  dropActive.value = false
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
useWatchedDirs(() => [desktopDir.value, trashFilesDir.value])

let offFs: (() => void) | undefined
onMounted(() => {
  void load()
  offFs = onFsChanged((dir) => {
    if (dir === desktopDir.value || dir === trashFilesDir.value) void load()
  })
})
onBeforeUnmount(() => offFs?.())
</script>

<template>
  <ContextMenuRoot>
    <ContextMenuTrigger as-child>
      <div
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
          :draggable="!!item.entry"
          :title="item.name"
          @click="select(item, $event)"
          @dblclick="item.open()"
          @contextmenu="onItemContext(item)"
          @dragstart="onDragStart(item, $event)"
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
          <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
        </template>
      </ContextMenuContent>
    </ContextMenuPortal>
  </ContextMenuRoot>
</template>

<style scoped>
.desktop-icons {
  position: absolute;
  inset: 0;
  display: grid;
  grid-auto-flow: column;
  grid-template-rows: repeat(auto-fill, var(--desktop-cell-h));
  grid-auto-columns: var(--desktop-cell-w);
  align-content: start;
  gap: 4px;
  padding: 8px;
  outline: none;
}
.desktop-icons.drop {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.desktop-icon {
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
