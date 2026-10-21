<script setup lang="ts">
// Files: a Nemo-style file manager. Every operation goes to the worker,
// which runs as the logged-in user, so the kernel decides what is allowed.
//
// Locations are absolute paths, plus "computer://" (drives) and "trash://"
// (the freedesktop trash, shared with the real Nemo).
//
// Copy, move and delete run as worker jobs (the shell shows progress);
// listings stream in and only visible items are rendered (ItemsView).
// Not yet: inotify-driven refresh (phase 4).
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from 'reka-ui'
import { baseName, dirName, fsApi, joinPath, searchStream, uniqueName } from '@/api/fs'
import type { Bookmark, Entry, Mount, Place, TrashItem } from '@/api/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { emitBookmarksChanged, emitFsChanged, onBookmarksChanged, onFsChanged } from '@/lib/bus'
import { useWatchedDirs } from '@/lib/fswatch'
import { dragPaths, dropOp, isNoopDrop, isPathDrag, setDragPaths, setDropEffect } from '@/lib/dnd'
import { deletePermanently, moveToTrash, paste, startJob } from '@/lib/fileops'
import { formatSize, mimeLabel } from '@/lib/format'
import ItemsView from './ItemsView.vue'
import { defaultZoom, sortItems, sortKeys, zoomLevels, type Item, type SortKey, type ViewMode } from './types'

const props = defineProps<{ ctx: AppContext }>()
const home = props.ctx.home

const COMPUTER = 'computer://'
const TRASH = 'trash://'

const location = ref(typeof props.ctx.args.path === 'string' ? props.ctx.args.path : home)
const backStack = ref<string[]>([])
const forwardStack = ref<string[]>([])
// shallowRef: a listing can hold 100 000 entries; deep reactivity on each
// would cost far more than the listing itself.
const entries = shallowRef<Entry[]>([])
const trashItems = shallowRef<TrashItem[]>([])
const mounts = shallowRef<Mount[]>([])
const places = ref<Place[]>([])
const bookmarks = ref<Bookmark[]>([])
/** The bookmark whose sidebar menu is open. */
const bookmarkTarget = ref<Bookmark | null>(null)
const loading = ref(false)
const error = ref('')
const showHidden = ref(props.ctx.args.showHidden === true)
const viewMode = ref<ViewMode>(props.ctx.args.view === 'list' ? 'list' : 'grid')
const sortKey = ref<SortKey>(sortKeys.find((k) => k === props.ctx.args.sort) ?? 'name')
const sortDesc = ref(props.ctx.args.sortDesc === true)
const zoom = ref<Record<ViewMode, number>>({
  grid: savedZoom('grid'),
  list: savedZoom('list'),
})
const view = ref<InstanceType<typeof ItemsView> | null>(null)

// Search (Ctrl+F): results replace the folder's items while a query is set.
const searchOpen = ref(false)
const query = ref('')
const searchResults = shallowRef<Entry[]>([])
const searching = ref(false)
const searchTruncated = ref(false)
const searchInput = ref<HTMLInputElement | null>(null)
const selected = ref(new Set<string>())
const renaming = ref<string | null>(null)
const renameValue = ref('')
const uploads = ref<{ id: number; name: string; loaded: number; total: number }[]>([])
const dropActive = ref(false)
const viewEl = ref<HTMLElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
let anchorKey: string | null = null
/** The item keyboard navigation moves from. */
let leadKey: string | null = null
let uploadSeq = 0

function savedZoom(mode: ViewMode): number {
  const v = props.ctx.args[mode === 'grid' ? 'zoomGrid' : 'zoomList']
  return typeof v === 'number' && v >= 0 && v < zoomLevels[mode].length ? v : defaultZoom[mode]
}

const iconSize = computed(() => zoomLevels[viewMode.value][zoom.value[viewMode.value]]!)

function setZoom(delta: number | null) {
  const mode = viewMode.value
  const max = zoomLevels[mode].length - 1
  const next = delta === null ? defaultZoom[mode] : Math.min(max, Math.max(0, zoom.value[mode] + delta))
  zoom.value = { ...zoom.value, [mode]: next }
  props.ctx.saveState({ [mode === 'grid' ? 'zoomGrid' : 'zoomList']: next })
}

function setSort(key: SortKey, desc?: boolean) {
  // Clicking the sorted column again reverses it, as in Nemo's list view.
  sortDesc.value = desc ?? (key === sortKey.value ? !sortDesc.value : false)
  sortKey.value = key
  props.ctx.saveState({ sort: key, sortDesc: sortDesc.value })
}

function mtimeMs(iso?: string) {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isNaN(t) ? 0 : t
}

const isPath = computed(() => location.value.startsWith('/'))
const isTrash = computed(() => location.value === TRASH)
const isComputer = computed(() => location.value === COMPUTER)
const trashFilesDir = computed(() => places.value.find((p) => p.id === 'trash')?.path ?? '')
const placeIcons = computed(() => new Map(places.value.filter((p) => p.id !== 'trash').map((p) => [p.path, p.icon])))

const searchActive = computed(() => searchOpen.value && isPath.value && query.value.trim() !== '')

const items = computed<Item[]>(() => {
  if (searchActive.value) {
    return searchResults.value.map((e) => ({
      key: e.path,
      name: e.name,
      icon: iconsForEntry(e, placeIcons.value),
      type: mimeLabel(e.mime),
      size: e.type === 'dir' ? undefined : e.size,
      mtime: e.mtime,
      mtimeMs: mtimeMs(e.mtime),
      location: dirName(e.path),
      entry: e,
    }))
  }
  if (isComputer.value) {
    return mounts.value.map((m) => ({
      key: m.mountPoint,
      name: m.mountPoint === '/' ? 'File System' : baseName(m.mountPoint),
      icon: ['drive-harddisk'],
      type: `${m.fsType} drive`,
      size: m.size,
      mtimeMs: 0,
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
      mtimeMs: mtimeMs(t.deletedAt),
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
      mtimeMs: mtimeMs(e.mtime),
      entry: e,
    }))
})

/** Items in display order: drives as the system lists them, the rest sorted. */
const sortLabels: Record<SortKey, string> = { name: 'Name', size: 'Size', type: 'Type', mtime: 'Modification Date' }

const sortedItems = computed(() =>
  isComputer.value ? items.value : sortItems(items.value, sortKey.value, sortDesc.value),
)

const selectedItems = computed(() => items.value.filter((i) => selected.value.has(i.key)))
/** Paths of selected files and folders (not trash items or drives). */
const selectedPaths = computed(() => selectedItems.value.flatMap((i) => (i.entry && !i.trash ? [i.entry.path] : [])))
const cutPaths = computed(() => new Set(props.ctx.clipboard.op === 'cut' ? props.ctx.clipboard.paths : []))
const canPaste = computed(() => isPath.value && !!props.ctx.clipboard.op)

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
  if (searchActive.value) {
    if (searching.value) return `Searching… ${n} found`
    return `${n === 1 ? '1 result' : `${n} results`}${searchTruncated.value ? ' (stopped at the first 5000)' : ''}`
  }
  return n === 1 ? '1 item' : `${n} items`
})

let loadAbort: AbortController | undefined
/** The folder whose listing is on screen, so a refresh can keep it until the new one is complete. */
let shownPath = ''

/** A change arrived while a listing was loading; load once more after it. */
let refreshQueued = false

async function load() {
  refreshQueued = false
  loadAbort?.abort()
  const ac = new AbortController()
  loadAbort = ac
  loading.value = true
  error.value = ''
  try {
    if (isComputer.value) mounts.value = (await fsApi.system()).mounts
    else if (isTrash.value) trashItems.value = (await fsApi.trashList()).items
    else await loadFolder(location.value, ac.signal)
  } catch (e) {
    if (ac.signal.aborted) return
    error.value = (e as Error).message
    entries.value = []
    trashItems.value = []
    shownPath = ''
  } finally {
    if (loadAbort === ac) {
      loading.value = false
      if (refreshQueued) void load()
    }
  }
}

/**
 * Reloads after a change. Unlike load it never cuts a running listing
 * short, so a folder that keeps changing (a big copy into it) still
 * refreshes a few times a second instead of never finishing.
 */
function refresh() {
  if (loading.value) refreshQueued = true
  else void load()
}

/**
 * Streams a folder listing. A new folder fills in as batches arrive (at most
 * four redraws a second); a refresh of the shown folder swaps in the new
 * listing only when it is complete, so nothing flickers.
 */
async function loadFolder(path: string, signal: AbortSignal) {
  const progressive = path !== shownPath
  const acc: Entry[] = []
  let last = 0
  if (progressive) entries.value = []
  await fsApi.listStream(
    path,
    (batch) => {
      acc.push(...batch)
      const now = performance.now()
      if (progressive && now - last > 250) {
        entries.value = acc.slice()
        last = now
      }
    },
    signal,
  )
  if (signal.aborted) return
  entries.value = acc
  shownPath = path
}

let searchAbort: AbortController | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined

watch([query, searchOpen, showHidden, location], () => {
  clearTimeout(searchTimer)
  searchAbort?.abort()
  searchResults.value = []
  // "Searching…" from the first keystroke, not "0 results" during the delay.
  searching.value = searchActive.value
  if (searchActive.value) searchTimer = setTimeout(runSearch, 300)
})

async function runSearch() {
  searchAbort?.abort()
  const ac = new AbortController()
  searchAbort = ac
  searching.value = true
  searchTruncated.value = false
  error.value = ''
  const acc: Entry[] = []
  let last = 0
  try {
    const end = await searchStream(
      location.value,
      query.value.trim(),
      showHidden.value,
      (batch) => {
        acc.push(...batch)
        if (performance.now() - last > 250) {
          searchResults.value = acc.slice()
          last = performance.now()
        }
      },
      ac.signal,
    )
    searchTruncated.value = !!end.truncated
  } catch (e) {
    if (!ac.signal.aborted) error.value = (e as Error).message
  } finally {
    if (!ac.signal.aborted) {
      searchResults.value = acc
      searching.value = false
    }
  }
}

function openSearch() {
  if (!isPath.value) return
  searchOpen.value = true
  void nextTick(() => searchInput.value?.select())
}

function closeSearch() {
  searchOpen.value = false
  query.value = ''
  viewEl.value?.focus()
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
  searchOpen.value = false
  query.value = ''
  location.value = to
  selected.value = new Set()
  renaming.value = null
  anchorKey = null
  leadKey = null
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
    const keys = sortedItems.value.map((i) => i.key)
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
  anchorKey = leadKey = item.key
}

/** Arrow keys, Home/End, PageUp/PageDown; Shift extends from the anchor. */
function moveSelection(to: (i: number) => number, extend: boolean) {
  const list = sortedItems.value
  if (!list.length) return
  const cur = leadKey ? list.findIndex((i) => i.key === leadKey) : -1
  const next = cur < 0 ? 0 : Math.min(list.length - 1, Math.max(0, to(cur)))
  const key = list[next]!.key
  if (extend && anchorKey) {
    const a = list.findIndex((i) => i.key === anchorKey)
    const [from, end] = a < next ? [a, next] : [next, a]
    selected.value = new Set(list.slice(from, end + 1).map((i) => i.key))
  } else {
    selected.value = new Set([key])
    anchorKey = key
  }
  leadKey = key
  view.value?.scrollToIndex(next)
}

function navKey(e: KeyboardEvent): boolean {
  const cols = viewMode.value === 'grid' ? (view.value?.cols ?? 1) : 1
  const page = view.value?.pageItems ?? 10
  const steps: Record<string, (i: number) => number> = {
    ArrowDown: (i) => i + cols,
    ArrowUp: (i) => i - cols,
    PageDown: (i) => i + page,
    PageUp: (i) => i - page,
    Home: () => 0,
    End: () => Number.MAX_SAFE_INTEGER,
  }
  if (cols > 1) {
    steps.ArrowRight = (i) => i + 1
    steps.ArrowLeft = (i) => i - 1
  }
  const step = steps[e.key]
  if (!step || e.altKey) return false
  moveSelection(step, e.shiftKey)
  return true
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
  if (isTrash.value) return deleteSelected()
  await moveToTrash(props.ctx, selectedPaths.value, trashFilesDir.value)
}

/** Shift+Delete, or Delete inside the trash: gone for good, after asking. */
async function deleteSelected() {
  const paths = isTrash.value
    ? selectedItems.value.flatMap((i) => (i.trash ? [i.trash.entry.path] : []))
    : selectedPaths.value
  await deletePermanently(props.ctx, paths)
}

function copySelected(op: 'copy' | 'cut') {
  if (selectedPaths.value.length) props.ctx.clipboard.set(op, selectedPaths.value)
}

async function pasteInto(dest = location.value) {
  if (isPath.value) await paste(props.ctx, dest)
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
  const ok = await props.ctx.confirm({
    title: 'Empty all items from the Trash?',
    message: 'All items in the Trash will be permanently deleted.',
    confirm: 'Empty Trash',
    danger: true,
  })
  if (!ok) return
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
  view.value?.scrollToIndex(sortedItems.value.indexOf(item))
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

/** Key of the folder item or place a drag hovers over, for highlighting. */
const dropTarget = ref<string | null>(null)

function onDragStart(item: Item, e: DragEvent) {
  if (!item.entry || item.trash) return e.preventDefault()
  if (!selected.value.has(item.key)) {
    selected.value = new Set([item.key])
    anchorKey = item.key
  }
  setDragPaths(e, selectedPaths.value)
}

/** Drops Tilde paths onto a folder (or the trash) and starts the job. */
async function dropPathsOn(dest: string, e: DragEvent) {
  const paths = dragPaths(e)
  if (dest === TRASH) return moveToTrash(props.ctx, paths, trashFilesDir.value)
  if (!paths.length || isNoopDrop(paths, dest)) return
  await startJob(props.ctx, dropOp(e), paths, dest)
}

/** dragover on a folder item or place: accept Tilde paths only. */
function onTargetDragOver(key: string, e: DragEvent) {
  if (!isPathDrag(e)) return
  e.preventDefault()
  e.stopPropagation()
  setDropEffect(e)
  dropTarget.value = key
}

function onTargetDrop(dest: string, e: DragEvent) {
  dropTarget.value = null
  dropActive.value = false
  if (!isPathDrag(e)) return
  e.preventDefault()
  e.stopPropagation()
  void dropPathsOn(dest, e)
}

function onViewDragOver(e: DragEvent) {
  const paths = isPathDrag(e)
  if (!(isPath.value || (paths && isTrash.value))) return
  e.preventDefault()
  if (paths) setDropEffect(e)
  dropActive.value = true
}

function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  void uploadFiles([...(input.files ?? [])])
  input.value = ''
}

function onDrop(e: DragEvent) {
  dropActive.value = false
  if (isPathDrag(e)) void dropPathsOn(location.value, e)
  else void uploadFiles([...(e.dataTransfer?.files ?? [])])
}

/** Opens the Properties window for the selection, or for this folder. */
function showProperties(paths = selectedPaths.value.length ? selectedPaths.value : [location.value]) {
  if (isPath.value || selectedPaths.value.length) props.ctx.launch('file-properties', { paths })
}

async function loadBookmarks() {
  try {
    bookmarks.value = await fsApi.bookmarks()
  } catch {
    // The section just stays empty.
  }
}

async function changeBookmarks(change: () => Promise<Bookmark[]>, what: string) {
  try {
    bookmarks.value = await change()
    emitBookmarksChanged()
  } catch (e) {
    props.ctx.notify(`Could not ${what}: ${(e as Error).message}`, 'error')
  }
}

/** Ctrl+D: bookmarks the selected folder, or the folder on show. */
function addBookmark() {
  const sel = selectedItems.value
  const path = sel.length === 1 && sel[0]!.entry?.type === 'dir' && !sel[0]!.trash ? sel[0]!.entry.path : location.value
  if (!path.startsWith('/')) return
  if (bookmarks.value.some((b) => b.path === path))
    return props.ctx.notify(`“${baseName(path)}” is already bookmarked.`)
  void changeBookmarks(() => fsApi.addBookmark(path), 'add the bookmark')
}

async function renameBookmark(b: Bookmark) {
  const name = await props.ctx.prompt({ title: 'Rename bookmark', value: b.name, confirm: 'Rename', label: 'Name' })
  if (name === null || !name.trim() || name === b.name) return
  void changeBookmarks(() => fsApi.addBookmark(b.path, name.trim()), 'rename the bookmark')
}

function removeBookmark(b: Bookmark) {
  void changeBookmarks(() => fsApi.removeBookmark(b.path), 'remove the bookmark')
}

function openTerminal(path?: string) {
  props.ctx.launch('terminal', { cwd: path ?? location.value })
}

function toggleHidden() {
  showHidden.value = !showHidden.value
  props.ctx.saveState({ showHidden: showHidden.value })
}

/** Ctrl+wheel zooms, as in Nemo. */
function onWheel(e: WheelEvent) {
  if (!e.ctrlKey) return
  e.preventDefault()
  setZoom(e.deltaY < 0 ? 1 : -1)
}

function setView(mode: ViewMode) {
  viewMode.value = mode
  props.ctx.saveState({ view: mode })
}

function onKey(e: KeyboardEvent) {
  if (renaming.value) return
  const alt = e.altKey
  const ctrl = e.ctrlKey || e.metaKey
  if (e.key === 'Enter' && alt) showProperties()
  else if (e.key === 'Enter') selectedItems.value.slice(0, 1).forEach(open)
  else if (e.key === 'Delete' && e.shiftKey) void deleteSelected()
  else if (e.key === 'Delete') void trashSelected()
  else if (ctrl && e.key.toLowerCase() === 'c') copySelected('copy')
  else if (ctrl && e.key.toLowerCase() === 'x') copySelected('cut')
  else if (ctrl && e.key.toLowerCase() === 'v') void pasteInto()
  else if (e.key === 'F2') startRename()
  else if (e.key === 'F5') void load()
  else if (e.key === 'Backspace' || (alt && e.key === 'ArrowUp')) goUp()
  else if (alt && e.key === 'ArrowLeft') goBack()
  else if (alt && e.key === 'ArrowRight') goForward()
  else if (ctrl && e.key.toLowerCase() === 'h') toggleHidden()
  else if (ctrl && e.key.toLowerCase() === 'f') openSearch()
  else if (ctrl && e.key.toLowerCase() === 'd') addBookmark()
  else if (ctrl && e.key.toLowerCase() === 'a') selected.value = new Set(items.value.map((i) => i.key))
  else if (ctrl && (e.key === '+' || e.key === '=')) setZoom(1)
  else if (ctrl && e.key === '-') setZoom(-1)
  else if (ctrl && e.key === '0') setZoom(null)
  else if (e.key === 'Escape') selected.value = new Set()
  else if (!navKey(e)) return
  e.preventDefault()
}

// The worker reports changes to the folder on screen (the trash's files
// folder for the Trash view) over the events socket.
useWatchedDirs(() => [isPath.value ? location.value : isTrash.value ? trashFilesDir.value : ''])

let offFs: (() => void) | undefined
let offBookmarks: (() => void) | undefined
onMounted(async () => {
  offFs = onFsChanged((dir) => {
    if (dir === location.value || (isTrash.value && dir === trashFilesDir.value)) refresh()
    if (searchActive.value && (dir === location.value || dir.startsWith(location.value + '/'))) void runSearch()
  })
  offBookmarks = onBookmarksChanged(loadBookmarks)
  void load()
  void loadBookmarks()
  try {
    places.value = await fsApi.places()
  } catch {
    // The sidebar stays empty; the view itself reports errors.
  }
  viewEl.value?.focus()
})
onBeforeUnmount(() => {
  offFs?.()
  offBookmarks?.()
  loadAbort?.abort()
})
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
      <button
        class="btn icon-only"
        :class="{ active: searchOpen }"
        title="Search (Ctrl+F)"
        :disabled="!isPath"
        @click="searchOpen ? closeSearch() : openSearch()"
      >
        <SymbolicIcon name="search" />
      </button>
      <div class="view-switch">
        <button
          class="btn icon-only"
          :class="{ active: viewMode === 'grid' }"
          title="Icon View"
          @click="setView('grid')"
        >
          <SymbolicIcon name="grid" />
        </button>
        <button
          class="btn icon-only"
          :class="{ active: viewMode === 'list' }"
          title="List View"
          @click="setView('list')"
        >
          <SymbolicIcon name="list" />
        </button>
      </div>
    </header>
    <div v-if="searchOpen" class="searchbar">
      <SymbolicIcon name="search" />
      <input
        ref="searchInput"
        v-model="query"
        class="entry"
        :placeholder="`Search in “${title}”`"
        aria-label="Search"
        @keydown.esc.prevent="closeSearch"
        @keydown.enter.prevent="viewEl?.focus()"
        @keydown.down.prevent="viewEl?.focus()"
      />
    </div>

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
          :class="{ active: location === placeTarget(p), target: dropTarget === `place:${p.id}` }"
          @click="navigate(placeTarget(p))"
          @dragover="onTargetDragOver(`place:${p.id}`, $event)"
          @dragleave="dropTarget = null"
          @drop="onTargetDrop(placeTarget(p), $event)"
        >
          <ThemeIcon :name="p.icon" :size="16" />
          <span>{{ p.name }}</span>
        </button>
        <template v-if="bookmarks.length">
          <h4>Bookmarks</h4>
          <ContextMenuRoot>
            <ContextMenuTrigger as-child>
              <div>
                <button
                  v-for="b in bookmarks"
                  :key="b.path"
                  class="place"
                  :class="{
                    active: location === b.path,
                    missing: !b.exists,
                    target: dropTarget === `bookmark:${b.path}`,
                  }"
                  :title="b.exists ? b.path : `${b.path} (not found)`"
                  @click="b.exists ? navigate(b.path) : ctx.notify(`“${b.path}” cannot be found.`, 'error')"
                  @contextmenu="bookmarkTarget = b"
                  @dragover="b.exists && onTargetDragOver(`bookmark:${b.path}`, $event)"
                  @dragleave="dropTarget = null"
                  @drop="onTargetDrop(b.path, $event)"
                >
                  <ThemeIcon :name="['folder']" :size="16" />
                  <span>{{ b.name }}</span>
                </button>
              </div>
            </ContextMenuTrigger>
            <ContextMenuPortal>
              <ContextMenuContent v-if="bookmarkTarget" class="popup-menu">
                <ContextMenuItem
                  class="item"
                  :disabled="!bookmarkTarget.exists"
                  @select="navigate(bookmarkTarget.path)"
                >
                  Open
                </ContextMenuItem>
                <ContextMenuItem class="item" @select="renameBookmark(bookmarkTarget)">Rename…</ContextMenuItem>
                <ContextMenuItem class="item" @select="removeBookmark(bookmarkTarget)">Remove</ContextMenuItem>
              </ContextMenuContent>
            </ContextMenuPortal>
          </ContextMenuRoot>
        </template>
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
            @dragover="onViewDragOver"
            @wheel="onWheel"
            @dragleave.self="dropActive = false"
            @drop.prevent="onDrop"
          >
            <ItemsView
              ref="view"
              v-model:rename-value="renameValue"
              :items="sortedItems"
              :mode="viewMode"
              :icon-size="iconSize"
              :selected="selected"
              :cut="cutPaths"
              :drop-target="dropTarget"
              :renaming="renaming"
              :sort-key="sortKey"
              :sort-desc="sortDesc"
              :date-label="isTrash ? 'Deleted' : 'Modified'"
              :show-location="searchActive"
              @item-click="click"
              @item-open="open"
              @item-context="onItemContext"
              @item-dragstart="onDragStart"
              @item-dragover="(item, e) => onTargetDragOver(item.key, e)"
              @item-dragleave="(item) => dropTarget === item.key && (dropTarget = null)"
              @item-drop="(item, e) => onTargetDrop(item.entry!.path, e)"
              @sort="setSort"
              @rename-commit="commitRename"
              @rename-cancel="cancelRename"
            />
            <p v-if="error" class="message error">{{ error }}</p>
            <p v-else-if="searchActive && !searching && items.length === 0" class="message dim">No results</p>
            <p v-else-if="!searchActive && !loading && items.length === 0" class="message dim">
              {{ isTrash ? 'Trash is empty' : 'This folder is empty' }}
            </p>
          </div>
        </ContextMenuTrigger>
        <ContextMenuPortal>
          <ContextMenuContent class="popup-menu">
            <template v-if="isTrash">
              <template v-if="selectedItems.length">
                <ContextMenuItem class="item" @select="restoreSelected">Restore</ContextMenuItem>
                <ContextMenuItem class="item" @select="deleteSelected">Delete Permanently…</ContextMenuItem>
              </template>
              <template v-else>
                <ContextMenuItem class="item" :disabled="!items.length" @select="emptyTrash">
                  Empty Trash
                </ContextMenuItem>
                <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
              </template>
            </template>
            <template v-else-if="isComputer">
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="open(selectedItems[0]!)">
                Open
              </ContextMenuItem>
              <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
            </template>
            <template v-else-if="selectedItems.length">
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="open(selectedItems[0]!)">
                Open
              </ContextMenuItem>
              <ContextMenuItem
                v-if="selectedItems.length === 1 && selectedItems[0]!.entry?.type === 'dir'"
                class="item"
                @select="openTerminal(selectedItems[0]!.entry!.path)"
              >
                Open in Terminal
              </ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem class="item" @select="copySelected('cut')">Cut</ContextMenuItem>
              <ContextMenuItem class="item" @select="copySelected('copy')">Copy</ContextMenuItem>
              <ContextMenuItem
                v-if="selectedItems.length === 1 && selectedItems[0]!.entry?.type === 'dir'"
                class="item"
                :disabled="!canPaste"
                @select="pasteInto(selectedItems[0]!.entry!.path)"
              >
                Paste Into Folder
              </ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem v-if="selectedItems.length === 1" class="item" @select="startRename()">
                Rename…
              </ContextMenuItem>
              <ContextMenuItem class="item" @select="trashSelected">Move to Trash</ContextMenuItem>
              <ContextMenuItem class="item" @select="deleteSelected">Delete Permanently…</ContextMenuItem>
              <template v-if="selectedItems.some((i) => i.entry?.type === 'file')">
                <ContextMenuSeparator class="separator" />
                <ContextMenuItem class="item" @select="downloadSelected">Download</ContextMenuItem>
              </template>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem
                v-if="selectedItems.length === 1 && selectedItems[0]!.entry?.type === 'dir'"
                class="item"
                @select="addBookmark"
              >
                Add to Bookmarks
              </ContextMenuItem>
              <ContextMenuItem class="item" @select="showProperties()">Properties</ContextMenuItem>
            </template>
            <template v-else>
              <ContextMenuItem class="item" @select="newFolder">New Folder</ContextMenuItem>
              <ContextMenuItem class="item" :disabled="!canPaste" @select="pasteInto()">Paste</ContextMenuItem>
              <ContextMenuItem class="item" @select="fileInput?.click()">Upload Files…</ContextMenuItem>
              <ContextMenuItem class="item" @select="openTerminal()">Open in Terminal</ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuSub>
                <ContextMenuSubTrigger class="item">Sort By</ContextMenuSubTrigger>
                <ContextMenuPortal>
                  <ContextMenuSubContent class="popup-menu">
                    <ContextMenuItem v-for="k in sortKeys" :key="k" class="item" @select="setSort(k, sortDesc)">
                      {{ sortKey === k ? '● ' : '' }}{{ sortLabels[k] }}
                    </ContextMenuItem>
                    <ContextMenuSeparator class="separator" />
                    <ContextMenuItem class="item" @select="setSort(sortKey, !sortDesc)">
                      {{ sortDesc ? '✓ ' : '' }}Reversed Order
                    </ContextMenuItem>
                  </ContextMenuSubContent>
                </ContextMenuPortal>
              </ContextMenuSub>
              <ContextMenuItem class="item" @select="setZoom(1)">Zoom In</ContextMenuItem>
              <ContextMenuItem class="item" @select="setZoom(-1)">Zoom Out</ContextMenuItem>
              <ContextMenuItem class="item" @select="setZoom(null)">Normal Size</ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem class="item" @select="toggleHidden">
                {{ showHidden ? '✓ ' : '' }}Show Hidden Files
              </ContextMenuItem>
              <ContextMenuItem class="item" @select="load">Refresh</ContextMenuItem>
              <ContextMenuSeparator class="separator" />
              <ContextMenuItem class="item" @select="addBookmark">Bookmark This Folder</ContextMenuItem>
              <ContextMenuItem class="item" @select="showProperties([location])">Properties</ContextMenuItem>
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
      <span class="zoom">
        <button class="btn flat icon-only" title="Zoom Out (Ctrl+−)" @click="setZoom(-1)">
          <SymbolicIcon name="zoom-out" />
        </button>
        <button class="btn flat icon-only" title="Zoom In (Ctrl++)" @click="setZoom(1)">
          <SymbolicIcon name="zoom-in" />
        </button>
      </span>
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
.toolbar > .active {
  background: var(--button-active-bg);
}
.searchbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: var(--headerbar-bg);
  border-bottom: 1px solid var(--border);
  color: var(--view-dim-fg);
}
.searchbar .entry {
  flex: 1;
  max-width: 480px;
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
  background: var(--view-bg);
  color: var(--view-fg);
  outline: none;
}
.view.drop {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.message {
  position: absolute;
  left: 0;
  right: 0;
  top: 40px;
  margin: 0;
  text-align: center;
  pointer-events: none;
}
.message.error {
  color: var(--danger);
}
.place.missing {
  opacity: 0.55;
}
.place.target {
  box-shadow: inset 0 0 0 2px var(--accent);
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
.statusbar .zoom {
  display: flex;
  margin-left: auto;
}
.statusbar .zoom .btn {
  min-width: 24px;
  margin: 0;
  padding: 0;
}
.statusbar .btn {
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
