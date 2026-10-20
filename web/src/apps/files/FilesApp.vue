<script setup lang="ts">
// Files: a Nemo-style file manager. Every operation goes to the worker,
// which runs as the logged-in user, so the kernel decides what is allowed.
//
// Locations are absolute paths, plus "computer://" (drives) and "trash://"
// (the freedesktop trash, shared with the real Nemo).
//
// Not yet: background copy/move jobs with progress and conflict prompts
// (docs/roadmap.md, phase 3), thumbnails, inotify-driven refresh, drag and
// drop between windows.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ContextMenuContent, ContextMenuItem, ContextMenuPortal, ContextMenuRoot, ContextMenuSeparator, ContextMenuTrigger } from 'reka-ui'
import { baseName, dirName, fsApi, joinPath, uniqueName } from '@/api/fs'
import type { Entry, Mount, Place, TrashItem } from '@/api/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { emitFsChanged, onFsChanged } from '@/lib/bus'
import { formatDate, formatSize, mimeLabel } from '@/lib/format'
import { useSession } from '@/stores/session'

const props = defineProps<{ ctx: AppContext }>()
const session = useSession()
const home = session.info?.home ?? '/'

const COMPUTER = 'computer://'
const TRASH = 'trash://'

interface Item {
  key: string
  name: string
  icon: string[]
  type: string
  size?: number
  mtime?: string
  entry?: Entry
  trash?: TrashItem
  mount?: Mount
}

const location = ref(typeof props.ctx.args.path === 'string' ? props.ctx.args.path : home)
const backStack = ref<string[]>([])
const forwardStack = ref<string[]>([])
const entries = ref<Entry[]>([])
const trashItems = ref<TrashItem[]>([])
const mounts = ref<Mount[]>([])
const places = ref<Place[]>([])
const loading = ref(false)
const error = ref('')
const showHidden = ref(props.ctx.args.showHidden === true)
const viewMode = ref<'grid' | 'list'>(props.ctx.args.view === 'list' ? 'list' : 'grid')
const selected = ref(new Set<string>())
const renaming = ref<string | null>(null)
const renameValue = ref('')
const uploads = ref<{ id: number; name: string; loaded: number; total: number }[]>([])
const dropActive = ref(false)
const viewEl = ref<HTMLElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
let anchorKey: string | null = null
let uploadSeq = 0

const isPath = computed(() => location.value.startsWith('/'))
const isTrash = computed(() => location.value === TRASH)
const isComputer = computed(() => location.value === COMPUTER)
const trashFilesDir = computed(() => places.value.find((p) => p.id === 'trash')?.path ?? '')
const placeIcons = computed(() => new Map(places.value.filter((p) => p.id !== 'trash').map((p) => [p.path, p.icon])))

const items = computed<Item[]>(() => {
  if (isComputer.value) {
    return mounts.value.map((m) => ({
      key: m.mountPoint,
      name: m.mountPoint === '/' ? 'File System' : baseName(m.mountPoint),
      icon: ['drive-harddisk'],
      type: `${m.fsType} drive`,
      size: m.size,
      mount: m,
    }))
  }
  if (isTrash.value) {
    return trashItems.value.map((t) => ({
      key: t.name,
      name: baseName(t.originalPath),
      icon: iconsForEntry(t.entry),
      type: mimeLabel(t.entry.mime),
      size: t.entry.type === 'dir' ? undefined : t.entry.size,
      mtime: t.deletedAt,
      entry: t.entry,
      trash: t,
    }))
  }
  return entries.value
    .filter((e) => showHidden.value || !e.hidden)
    .map((e) => ({
      key: e.path,
      name: e.name,
      icon: iconsForEntry(e, placeIcons.value),
      type: mimeLabel(e.mime),
      size: e.type === 'dir' ? undefined : e.size,
      mtime: e.mtime,
      entry: e,
    }))
})

const selectedItems = computed(() => items.value.filter((i) => selected.value.has(i.key)))

const title = computed(() => {
  if (isComputer.value) return 'Computer'
  if (isTrash.value) return 'Trash'
  if (location.value === home) return 'Home'
  if (location.value === '/') return 'File System'
  return baseName(location.value)
})
watch(title, (t) => props.ctx.setTitle(t), { immediate: true })

interface Crumb {
  label: string
  path: string
  icon?: string
}
const crumbs = computed<Crumb[]>(() => {
  if (isComputer.value) return [{ label: 'Computer', path: COMPUTER, icon: 'computer' }]
  if (isTrash.value) return [{ label: 'Trash', path: TRASH, icon: 'user-trash' }]
  const p = location.value
  const out: Crumb[] = []
  let rest: string
  let prefix: string
  if (p === home || p.startsWith(home + '/')) {
    out.push({ label: baseName(home), path: home, icon: 'user-home' })
    prefix = home
    rest = p.slice(home.length)
  } else {
    out.push({ label: '', path: '/', icon: 'drive-harddisk' })
    prefix = ''
    rest = p
  }
  for (const part of rest.split('/').filter(Boolean)) {
    prefix = `${prefix}/${part}`
    out.push({ label: part, path: prefix })
  }
  return out
})

const statusText = computed(() => {
  const sel = selectedItems.value
  if (sel.length === 1) {
    const s = sel[0]!
    return s.size !== undefined ? `"${s.name}" selected (${formatSize(s.size)})` : `"${s.name}" selected`
  }
  if (sel.length > 1) return `${sel.length} items selected`
  const n = items.value.length
  return n === 1 ? '1 item' : `${n} items`
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (isComputer.value) mounts.value = (await fsApi.system()).mounts
    else if (isTrash.value) trashItems.value = (await fsApi.trashList()).items
    else entries.value = (await fsApi.list(location.value)).entries
  } catch (e) {
    error.value = (e as Error).message
    entries.value = []
    trashItems.value = []
  } finally {
    loading.value = false
  }
}

function navigate(to: string, push = true) {
  if (to === location.value) {
    void load()
    return
  }
  if (push) {
    backStack.value.push(location.value)
    forwardStack.value = []
  }
  location.value = to
  selected.value = new Set()
  renaming.value = null
  anchorKey = null
  props.ctx.saveState({ path: to })
  void load()
  viewEl.value?.focus()
}

function goBack() {
  const prev = backStack.value.pop()
  if (prev === undefined) return
  forwardStack.value.push(location.value)
  navigate(prev, false)
}

function goForward() {
  const next = forwardStack.value.pop()
  if (next === undefined) return
  backStack.value.push(location.value)
  navigate(next, false)
}

function goUp() {
  if (isPath.value && location.value !== '/') navigate(dirName(location.value))
}

function placeTarget(p: Place) {
  return p.id === 'trash' ? TRASH : p.path
}

function open(item: Item) {
  if (item.mount) navigate(item.mount.mountPoint)
  else if (item.trash) props.ctx.notify('Restore the item to open it.')
  else if (item.entry?.type === 'dir') navigate(item.entry.path)
  else if (item.entry) props.ctx.openEntry(item.entry)
}

function click(item: Item, e: MouseEvent) {
  viewEl.value?.focus()
  if (e.shiftKey && anchorKey) {
    const keys = items.value.map((i) => i.key)
    const a = keys.indexOf(anchorKey)
    const b = keys.indexOf(item.key)
    const [from, to] = a < b ? [a, b] : [b, a]
    selected.value = new Set(keys.slice(from, to + 1))
    return
  }
  if (e.ctrlKey) {
    const s = new Set(selected.value)
    if (s.has(item.key)) s.delete(item.key)
    else s.add(item.key)
    selected.value = s
  } else {
    selected.value = new Set([item.key])
  }
  anchorKey = item.key
}

function onItemContext(item: Item) {
  if (!selected.value.has(item.key)) {
    selected.value = new Set([item.key])
    anchorKey = item.key
  }
}

function onBackgroundContext(e: MouseEvent) {
  if (!(e.target as Element).closest('[data-item]')) selected.value = new Set()
}

function onBackgroundPointer(e: PointerEvent) {
  if (!(e.target as Element).closest('[data-item]')) selected.value = new Set()
}

async function trashSelected() {
  const paths = selectedItems.value.flatMap((i) => (i.entry && !i.trash ? [i.entry.path] : []))
  if (!paths.length) return
  const results = await fsApi.trash(paths)
  for (const r of results) {
    if (r.code === 'other_drive') props.ctx.notify(`${baseName(r.path)} is on another drive; trashing it there is not supported yet.`, 'error')
    else if (r.error) props.ctx.notify(`Could not move ${baseName(r.path)} to the trash: ${r.error}`, 'error')
  }
  emitFsChanged(location.value, trashFilesDir.value)
}

async function restoreSelected() {
  const chosen = selectedItems.value.flatMap((i) => (i.trash ? [i.trash] : []))
  if (!chosen.length) return
  try {
    await fsApi.restore(chosen.map((t) => t.name))
  } catch (e) {
    props.ctx.notify(`Could not restore: ${(e as Error).message}`, 'error')
  }
  emitFsChanged(trashFilesDir.value, ...chosen.map((t) => dirName(t.originalPath)))
}

async function emptyTrash() {
  if (!window.confirm('Empty all items from the Trash? They will be permanently deleted.')) return
  try {
    await fsApi.emptyTrash()
  } catch (e) {
    props.ctx.notify(`Could not empty the trash: ${(e as Error).message}`, 'error')
  }
  emitFsChanged(trashFilesDir.value)
}

async function newFolder() {
  if (!isPath.value) return
  const name = uniqueName('Untitled Folder', new Set(entries.value.map((e) => e.name)))
  try {
    const created = await fsApi.mkdir(joinPath(location.value, name))
    await load()
    selected.value = new Set([created.path])
    startRename(created.path)
    emitFsChanged(location.value)
  } catch (e) {
    props.ctx.notify(`Could not create a folder: ${(e as Error).message}`, 'error')
  }
}

function startRename(key?: string) {
  const item = key ? items.value.find((i) => i.key === key) : selectedItems.value[0]
  if (!item?.entry || item.trash) return
  renaming.value = item.key
  renameValue.value = item.name
  void nextTick(() => {
    const input = viewEl.value?.querySelector<HTMLInputElement>('input.rename')
    if (!input) return
    input.focus()
    // Select the name without its extension, as Nemo does.
    const dot = item.entry!.type === 'dir' ? -1 : item.name.lastIndexOf('.')
    input.setSelectionRange(0, dot > 0 ? dot : item.name.length)
  })
}

async function commitRename() {
  const key = renaming.value
  renaming.value = null
  const item = items.value.find((i) => i.key === key)
  const name = renameValue.value.trim()
  if (!item?.entry || !name || name === item.name) return
  if (name.includes('/')) {
    props.ctx.notify('File names cannot contain "/".', 'error')
    return
  }
  try {
    const renamed = await fsApi.rename(item.entry.path, joinPath(dirName(item.entry.path), name))
    await load()
    selected.value = new Set([renamed.path])
    emitFsChanged(location.value)
  } catch (e) {
    props.ctx.notify(`Could not rename: ${(e as Error).message}`, 'error')
  }
  viewEl.value?.focus()
}

function cancelRename() {
  renaming.value = null
  viewEl.value?.focus()
}

function downloadSelected() {
  for (const i of selectedItems.value) {
    if (i.entry?.type !== 'file') continue
    const a = document.createElement('a')
    a.href = fsApi.contentUrl(i.entry.path, true)
    a.download = i.name
    a.click()
  }
}

async function uploadFiles(files: File[]) {
  if (!isPath.value || !files.length) return
  const dir = location.value
  await Promise.all(
    files.map(async (f) => {
      const id = uploadSeq++
      uploads.value.push({ id, name: f.name, loaded: 0, total: f.size })
      try {
        await fsApi.upload(joinPath(dir, f.name), f, (loaded, total) => {
          const u = uploads.value.find((x) => x.id === id)
          if (u) Object.assign(u, { loaded, total })
        })
      } catch (e) {
        props.ctx.notify(`Could not upload ${f.name}: ${(e as Error).message}`, 'error')
      } finally {
        uploads.value = uploads.value.filter((x) => x.id !== id)
      }
    }),
  )
  emitFsChanged(dir)
}

function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  void uploadFiles([...(input.files ?? [])])
  input.value = ''
}

function onDrop(e: DragEvent) {
  dropActive.value = false
  void uploadFiles([...(e.dataTransfer?.files ?? [])])
}

function openTerminal(path?: string) {
  props.ctx.launch('terminal', { cwd: path ?? location.value })
}

function toggleHidden() {
  showHidden.value = !showHidden.value
  props.ctx.saveState({ showHidden: showHidden.value })
}

function setView(mode: 'grid' | 'list') {
  viewMode.value = mode
  props.ctx.saveState({ view: mode })
}

function onKey(e: KeyboardEvent) {
  if (renaming.value) return
  const alt = e.altKey
  const ctrl = e.ctrlKey || e.metaKey
  if (e.key === 'Enter') selectedItems.value.slice(0, 1).forEach(open)
  else if (e.key === 'Delete') void trashSelected()
  else if (e.key === 'F2') startRename()
  else if (e.key === 'F5') void load()
  else if (e.key === 'Backspace' || (alt && e.key === 'ArrowUp')) goUp()
  else if (alt && e.key === 'ArrowLeft') goBack()
  else if (alt && e.key === 'ArrowRight') goForward()
  else if (ctrl && e.key.toLowerCase() === 'h') toggleHidden()
  else if (ctrl && e.key.toLowerCase() === 'a') selected.value = new Set(items.value.map((i) => i.key))
  else if (e.key === 'Escape') selected.value = new Set()
  else return
  e.preventDefault()
}

let offFs: (() => void) | undefined
onMounted(async () => {
  offFs = onFsChanged((dir) => {
    if (dir === location.value || (isTrash.value && dir === trashFilesDir.value)) void load()
  })
  void load()
  try {
    places.value = await fsApi.places()
  } catch {
    // The sidebar stays empty; the view itself reports errors.
  }
  viewEl.value?.focus()
})
onBeforeUnmount(() => offFs?.())
</script>

<template>
  <div class="files">
    <header class="toolbar">
      <button class="btn icon-only" title="Back" :disabled="!backStack.length" @click="goBack">
        <SymbolicIcon name="back" />
      </button>
      <button class="btn icon-only" title="Forward" :disabled="!forwardStack.length" @click="goForward">
        <SymbolicIcon name="forward" />
      </button>
      <button class="btn icon-only" title="Up" :disabled="!isPath || location === '/'" @click="goUp">
        <SymbolicIcon name="up" />
      </button>
      <nav class="pathbar" aria-label="Location">
        <button
          v-for="(c, i) in crumbs"
          :key="c.path"
          class="crumb"
          :class="{ current: i === crumbs.length - 1 }"
          @click="navigate(c.path)"
        >
          <ThemeIcon v-if="c.icon" :name="c.icon" :size="16" />
          <span v-if="c.label">{{ c.label }}</span>
        </button>
      </nav>
      <div class="view-switch">
        <button class="btn icon-only" :class="{ active: viewMode === 'grid' }" title="Icon View" @click="setView('grid')">
          <SymbolicIcon name="grid" />
        </button>
        <button class="btn icon-only" :class="{ active: viewMode === 'list' }" title="List View" @click="setView('list')">
          <SymbolicIcon name="list" />
        </button>
      </div>
    </header>

    <div class="body">
      <aside class="sidebar">
        <h4>My Computer</h4>
        <button class="place" :class="{ active: isComputer }" @click="navigate(COMPUTER)">
          <ThemeIcon name="computer" :size="16" />
          <span>Computer</span>
        </button>
        <button
          v-for="p in places"
          :key="p.id"
          class="place"
          :class="{ active: location === placeTarget(p) }"
          @click="navigate(placeTarget(p))"
        >
          <ThemeIcon :name="p.icon" :size="16" />
          <span>{{ p.name }}</span>
        </button>
      </aside>

      <ContextMenuRoot>
        <ContextMenuTrigger as-child>
          <div
            ref="viewEl"
            class="view"
            :class="[viewMode, { drop: dropActive }]"
            tabindex="0"
            @keydown="onKey"
            @pointerdown="onBackgroundPointer"
            @contextmenu="onBackgroundContext"
            @dragover.prevent="isPath && (dropActive = true)"
            @dragleave.self="dropActive = false"
            @drop.prevent="onDrop"
          >
            <p v-if="error" class="message error">{{ error }}</p>
            <p v-else-if="!loading && items.length === 0" class="message dim">
              {{ isTrash ? 'Trash is empty' : 'This folder is empty' }}
            </p>

            <template v-if="viewMode === 'grid'">
              <div
                v-for="item in items"
                :key="item.key"
                data-item
                class="cell"
                :class="{ selected: selected.has(item.key), hidden: item.entry?.hidden }"
                :title="item.name"
                @click="click(item, $event)"
                @dblclick="open(item)"
                @contextmenu="onItemContext(item)"
              >
                <ThemeIcon :name="item.icon" :size="48" />
                <input
                  v-if="renaming === item.key"
                  v-model="renameValue"
                  class="rename entry"
                  @keydown.enter.stop.prevent="commitRename"
                  @keydown.esc.stop.prevent="cancelRename"
                  @blur="commitRename"
                  @click.stop
                  @dblclick.stop
                />
                <span v-else class="label">{{ item.name }}</span>
              </div>
            </template>

            <table v-else class="list">
              <thead>
                <tr>
                  <th class="name">Name</th>
                  <th class="size">Size</th>
                  <th class="type">Type</th>
                  <th class="date">{{ isTrash ? 'Deleted' : 'Modified' }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="item in items"
                  :key="item.key"
                  data-item
                  :class="{ selected: selected.has(item.key), hidden: item.entry?.hidden }"
                  @click="click(item, $event)"
                  @dblclick="open(item)"
                  @contextmenu="onItemContext(item)"
                >
                  <td class="name">
                    <ThemeIcon :name="item.icon" :size="16" />
                    <input
                      v-if="renaming === item.key"
                      v-model="renameValue"
                      class="rename entry"
                      @keydown.enter.stop.prevent="commitRename"
                      @keydown.esc.stop.prevent="cancelRename"
                      @blur="commitRename"
                      @click.stop
                      @dblclick.stop
                    />
                    <span v-else>{{ item.name }}</span>
                  </td>
                  <td class="size">{{ item.size !== undefined ? formatSize(item.size) : '' }}</td>
                  <td class="type">{{ item.type }}</td>
                  <td class="date">{{ item.mtime ? formatDate(item.mtime) : '' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </ContextMenuTrigger>
        <ContextMenuPortal>
          <ContextMenuContent class="popup-menu">
            <template v-if="isTrash">
              <template v-if="selectedItems.length">
                <ContextMenuItem class="item" @select="restoreSelected">Restore</ContextMenuItem>
              </template>
              <template v-else>
                <ContextMenuItem class="item" :disabled="!items.length" @select="emptyTrash">Empty Trash</ContextMenuItem>
                <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
              </template>
            </template>
            <template v-else-if="isComputer">
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="open(selectedItems[0]!)">Open</ContextMenuItem>
              <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
            </template>
            <template v-else-if="selectedItems.length">
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="open(selectedItems[0]!)">Open</ContextMenuItem>
              <ContextMenuItem
                v-if="selectedItems.length === 1 && selectedItems[0]!.entry?.type === 'dir'"
                class="item"
                @select="openTerminal(selectedItems[0]!.entry!.path)"
              >
                Open in Terminal
              </ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="startRename()">Rename…</ContextMenuItem>
              <ContextMenuItem class="item" @select="trashSelected">Move to Trash</ContextMenuItem>
              <template v-if="selectedItems.some((i) => i.entry?.type === 'file')">
                <ContextMenuSeparator class="separator" />
                <ContextMenuItem class="item" @select="downloadSelected">Download</ContextMenuItem>
              </template>
            </template>
            <template v-else>
              <ContextMenuItem class="item" @select="newFolder">New Folder</ContextMenuItem>
              <ContextMenuItem class="item" @select="fileInput?.click()">Upload Files…</ContextMenuItem>
              <ContextMenuItem class="item" @select="openTerminal()">Open in Terminal</ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem class="item" @select="toggleHidden">
                {{ showHidden ? '✓ ' : '' }}Show Hidden Files
              </ContextMenuItem>
              <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
            </template>
          </ContextMenuContent>
        </ContextMenuPortal>
      </ContextMenuRoot>
    </div>

    <footer class="statusbar">
      <span>{{ statusText }}</span>
      <span v-for="u in uploads" :key="u.id" class="upload">
        Uploading {{ u.name }}
        <progress :max="u.total || 1" :value="u.loaded" />
      </span>
      <button v-if="isTrash && items.length" class="btn" @click="emptyTrash">Empty Trash</button>
    </footer>
    <input ref="fileInput" type="file" multiple hidden @change="onPickFiles" />
  </div>
</template>

<style scoped>
.files {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  background: var(--window-bg);
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px;
  background: var(--headerbar-bg);
  border-bottom: 1px solid var(--border);
}
.pathbar {
  flex: 1;
  min-width: 0;
  display: flex;
  overflow: hidden;
  margin: 0 6px;
}
.crumb {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 10px;
  border: 1px solid var(--button-border);
  border-right-width: 0;
  background: var(--button-bg);
  color: var(--button-fg);
  white-space: nowrap;
}
.crumb:first-child {
  border-radius: var(--radius) 0 0 var(--radius);
}
.crumb:last-child {
  border-right-width: 1px;
  border-radius: 0 var(--radius) var(--radius) 0;
}
.crumb:hover {
  background: var(--button-hover-bg);
}
.crumb.current {
  background: var(--button-active-bg);
  font-weight: bold;
}
.view-switch {
  display: flex;
  gap: 2px;
}
.view-switch .active {
  background: var(--button-active-bg);
}

.body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.sidebar {
  width: 180px;
  flex: none;
  overflow-y: auto;
  padding: 6px 0;
  background: var(--sidebar-bg);
  color: var(--sidebar-fg);
  border-right: 1px solid var(--border);
}
.sidebar h4 {
  margin: 6px 12px 4px;
  font-size: var(--font-size-small);
  color: var(--view-dim-fg);
}
.place {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 5px 12px 5px 16px;
  border: none;
  background: transparent;
  color: inherit;
  text-align: left;
}
.place span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.place:hover {
  background: var(--hover-bg);
}
.place.active {
  background: var(--selection-bg);
  color: var(--selection-fg);
}

.view {
  position: relative;
  flex: 1;
  min-width: 0;
  overflow: auto;
  background: var(--view-bg);
  color: var(--view-fg);
  outline: none;
}
.view.drop {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.view.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, 104px);
  grid-auto-rows: min-content;
  align-content: start;
  gap: 4px;
  padding: 8px;
}
.message {
  grid-column: 1 / -1;
  margin: 24px;
  text-align: center;
}
.message.error {
  color: var(--danger);
}

.cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 6px 4px;
  border-radius: 4px;
}
.cell.hidden {
  opacity: 0.65;
}
.cell:hover {
  background: var(--hover-bg);
}
.cell.selected {
  background: var(--selection-unfocused-bg);
}
.view:focus .cell.selected {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.cell .label {
  max-width: 100%;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  text-align: center;
  line-height: 1.25;
}
.cell.selected .label {
  -webkit-line-clamp: 6;
}
.cell .rename {
  width: 100%;
  min-height: 24px;
  padding: 2px 4px;
  text-align: center;
}

.list {
  width: 100%;
  border-collapse: collapse;
}
.list th {
  position: sticky;
  top: 0;
  padding: 5px 8px;
  background: var(--window-bg);
  border-bottom: 1px solid var(--border);
  font-weight: normal;
  text-align: left;
  color: var(--view-dim-fg);
}
.list td {
  padding: 3px 8px;
  white-space: nowrap;
}
.list td.name {
  display: flex;
  align-items: center;
  gap: 8px;
}
.list td.size,
.list th.size {
  text-align: right;
}
.list td.size,
.list td.type,
.list td.date {
  color: var(--view-dim-fg);
}
.list tr.hidden {
  opacity: 0.65;
}
.list tbody tr:hover {
  background: var(--hover-bg);
}
.list tr.selected {
  background: var(--selection-unfocused-bg);
}
.view:focus .list tr.selected,
.view:focus .list tr.selected td {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.list .rename {
  min-height: 22px;
  padding: 1px 4px;
}

.statusbar {
  display: flex;
  align-items: center;
  gap: 16px;
  min-height: 28px;
  padding: 2px 10px;
  background: var(--window-bg);
  border-top: 1px solid var(--border);
  font-size: var(--font-size-small);
}
.statusbar .btn {
  margin-left: auto;
  min-height: 22px;
  padding: 1px 8px;
}
.upload {
  display: flex;
  align-items: center;
  gap: 6px;
}
.upload progress {
  width: 100px;
  accent-color: var(--accent);
}
</style>
