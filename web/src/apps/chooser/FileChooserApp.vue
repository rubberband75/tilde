<script setup lang="ts">
// The shared file chooser (D49), after GTK's: places on the left, the
// folder as a path bar, its files in a list, a name entry when saving and
// a filter menu. It opens as a modal dialog window of the app that asked
// (ctx.chooseFile) and answers through stores/fileChooser.ts. Everything
// it lists comes from the worker, so it shows what the user may see.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { ContextMenuContent, ContextMenuItem, ContextMenuPortal, ContextMenuRoot, ContextMenuTrigger } from 'reka-ui'
import { ApiError } from '@/api/client'
import { baseName, fsApi, joinPath, uniqueName } from '@/api/fs'
import type { Bookmark, Entry, Place, Volume } from '@/api/types'
import ItemsView from '@/apps/files/ItemsView.vue'
import { sortItems, type Item, type SortKey } from '@/apps/files/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { onBookmarksChanged, onFsChanged } from '@/lib/bus'
import { mimeLabel } from '@/lib/format'
import { useWatchedDirs } from '@/lib/fswatch'
import { useFileChooser, type ChooseFileOptions } from '@/stores/fileChooser'
import { badName, filterAccepts, resolveTyped, splitTyped } from './chooser'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx
const chooser = useFileChooser()

const opts = (ctx.args.options ?? { mode: 'open' }) as ChooseFileOptions
const mode = opts.mode
const multiple = mode === 'open' && !!opts.multiple
const filters = opts.filters ?? []

const defaults = {
  open: { title: 'Open File', accept: 'Open' },
  save: { title: 'Save File', accept: 'Save' },
  folder: { title: 'Select Folder', accept: 'Select' },
}[mode]
ctx.setTitle(opts.title ?? defaults.title)
ctx.setIcon('folder-open')

const HIDDEN_KEY = 'tilde:chooser:hidden'
function storedHidden(): boolean {
  try {
    return localStorage.getItem(HIDDEN_KEY) === '1'
  } catch {
    return false
  }
}

const folder = ref(opts.folder || chooser.lastFolder || ctx.home)
const entries = shallowRef<Entry[]>([])
const loading = ref(false)
const error = ref('')
const showHidden = ref(storedHidden())
const filterIndex = ref(0)
const sortKey = ref<SortKey>('name')
const sortDesc = ref(false)
const selected = ref(new Set<string>())
let anchorKey = ''
let leadKey = ''
const name = ref(opts.name ?? '')
const typing = ref(false)
const typed = ref('')
const busy = ref(false)

const places = ref<Place[]>([])
const bookmarks = ref<Bookmark[]>([])
const volumes = ref<Volume[]>([])

const view = ref<InstanceType<typeof ItemsView> | null>(null)
const viewEl = ref<HTMLElement | null>(null)
const nameInput = ref<HTMLInputElement | null>(null)
const locationInput = ref<HTMLInputElement | null>(null)

const placeIcons = computed(() => new Map(places.value.map((p) => [p.path, p.icon])))
const filter = computed(() => filters[filterIndex.value])

const items = computed<Item[]>(() => {
  const list = entries.value
    .filter((e) => showHidden.value || !e.hidden)
    .filter((e) => (mode === 'folder' ? e.type === 'dir' : filterAccepts(filter.value, e)))
    .map((e) => ({
      key: e.path,
      name: e.name,
      icon: iconsForEntry(e, placeIcons.value),
      type: mimeLabel(e.mime),
      size: e.type === 'dir' ? undefined : e.size,
      mtime: e.mtime,
      mtimeMs: Date.parse(e.mtime) || 0,
      entry: e,
    }))
  return sortItems(list, sortKey.value, sortDesc.value)
})
const selectedEntries = computed(() =>
  items.value.flatMap((i) => (selected.value.has(i.key) && i.entry ? [i.entry] : [])),
)

/** The path bar: Home and what is below it, or the file system root. */
const crumbs = computed(() => {
  const home = ctx.home
  const underHome = home !== '/' && (folder.value === home || folder.value.startsWith(`${home}/`))
  const base = underHome ? home : '/'
  const out = [{ path: base, label: underHome ? baseName(home) : '', icon: underHome ? 'user-home' : 'drive-harddisk' }]
  const rest = folder.value.slice(base.length).split('/').filter(Boolean)
  let path = base
  for (const part of rest) {
    path = joinPath(path, part)
    out.push({ path, label: part, icon: '' })
  }
  return out
})

const sidebar = computed(() =>
  places.value.filter((p) => p.id !== 'trash').map((p) => ({ path: p.path, name: p.name, icon: [p.icon] })),
)
/** Mounted removable drives, as in Files' Devices. */
const devices = computed(() => volumes.value.filter((v) => v.mountPoint && v.canEject))

let loadAbort: AbortController | undefined

/** Lists the folder; keep holds the selection (a refresh after a change). */
async function load(keep = false) {
  loadAbort?.abort()
  const ac = new AbortController()
  loadAbort = ac
  const path = folder.value
  loading.value = true
  error.value = ''
  if (!keep) entries.value = []
  try {
    const acc: Entry[] = []
    await fsApi.listStream(path, (batch) => acc.push(...batch), ac.signal)
    if (ac.signal.aborted) return
    entries.value = acc
    if (keep) {
      const paths = new Set(acc.map((e) => e.path))
      selected.value = new Set([...selected.value].filter((k) => paths.has(k)))
    }
  } catch (e) {
    if (ac.signal.aborted) return
    entries.value = []
    error.value =
      e instanceof ApiError && e.code === 'permission_denied'
        ? `You do not have permission to see the contents of “${baseName(path)}”.`
        : (e as Error).message
  } finally {
    if (loadAbort === ac) loading.value = false
  }
}

function navigate(path: string) {
  typing.value = false
  if (path === folder.value) return
  folder.value = path
  selected.value = new Set()
  anchorKey = leadKey = ''
}
watch(folder, () => void load())

useWatchedDirs(() => [folder.value])
const offFs = onFsChanged((dir) => {
  if (dir === folder.value) void load(true)
})
const offBookmarks = onBookmarksChanged(() => void loadBookmarks())

async function loadBookmarks() {
  bookmarks.value = await fsApi.bookmarks().catch(() => [])
}

function toggleHidden() {
  showHidden.value = !showHidden.value
  try {
    localStorage.setItem(HIDDEN_KEY, showHidden.value ? '1' : '0')
  } catch {
    // A preference of this browser only; fine to lose.
  }
}

function setSort(key: SortKey) {
  if (sortKey.value === key) sortDesc.value = !sortDesc.value
  else {
    sortKey.value = key
    sortDesc.value = false
  }
}

function click(item: Item, e: MouseEvent) {
  viewEl.value?.focus()
  if (multiple && e.shiftKey && anchorKey) {
    const keys = items.value.map((i) => i.key)
    const a = keys.indexOf(anchorKey)
    const b = keys.indexOf(item.key)
    const [from, to] = a < b ? [a, b] : [b, a]
    selected.value = new Set(keys.slice(from, to + 1))
  } else if (multiple && e.ctrlKey) {
    const s = new Set(selected.value)
    if (s.has(item.key)) s.delete(item.key)
    else s.add(item.key)
    selected.value = s
    anchorKey = item.key
  } else {
    selected.value = new Set([item.key])
    anchorKey = item.key
  }
  leadKey = item.key
  if (mode === 'save' && item.entry?.type !== 'dir') name.value = item.name
}

/** Double click or Enter on an item: into a folder, or choose a file. */
function activate(item: Item) {
  const e = item.entry
  if (!e) return
  if (e.type === 'dir') navigate(e.path)
  else if (mode === 'open') finish([e.path])
  else if (mode === 'save') {
    name.value = e.name
    void accept()
  }
}

function moveSelection(to: (i: number) => number, extend: boolean) {
  const list = items.value
  if (!list.length) return
  const cur = leadKey ? list.findIndex((i) => i.key === leadKey) : -1
  const next = cur < 0 ? 0 : Math.min(list.length - 1, Math.max(0, to(cur)))
  const item = list[next]!
  if (extend && multiple && anchorKey) {
    const a = list.findIndex((i) => i.key === anchorKey)
    const [from, end] = a < next ? [a, next] : [next, a]
    selected.value = new Set(list.slice(from, end + 1).map((i) => i.key))
  } else {
    selected.value = new Set([item.key])
    anchorKey = item.key
    if (mode === 'save' && item.entry?.type !== 'dir') name.value = item.name
  }
  leadKey = item.key
  view.value?.scrollToIndex(next)
}

function onViewKey(e: KeyboardEvent) {
  const page = view.value?.pageItems ?? 10
  const steps: Record<string, (i: number) => number> = {
    ArrowDown: (i) => i + 1,
    ArrowUp: (i) => i - 1,
    PageDown: (i) => i + page,
    PageUp: (i) => i - page,
    Home: () => 0,
    End: () => Number.MAX_SAFE_INTEGER,
  }
  const step = steps[e.key]
  if (step && !e.altKey && !e.ctrlKey) {
    e.preventDefault()
    moveSelection(step, e.shiftKey)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    const lead = items.value.find((i) => i.key === leadKey && selected.value.has(i.key))
    if (lead && (lead.entry?.type === 'dir' || selectedEntries.value.length === 1)) activate(lead)
    else void accept()
  } else if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
    e.preventDefault()
    goUp()
  } else if (multiple && e.ctrlKey && e.key.toLowerCase() === 'a') {
    e.preventDefault()
    selected.value = new Set(items.value.map((i) => i.key))
  } else if (mode !== 'save' && e.key.length === 1 && !e.ctrlKey && !e.altKey && !e.metaKey && e.key !== ' ') {
    // Typing starts the location entry, as in GTK.
    e.preventDefault()
    startTyping(e.key)
  }
}

function goUp() {
  if (folder.value !== '/') navigate(folder.value.slice(0, folder.value.lastIndexOf('/')) || '/')
}

function startTyping(text = '') {
  if (mode === 'save') {
    nameInput.value?.focus()
    return
  }
  typing.value = true
  typed.value = text
  void nextTick(() => {
    const el = locationInput.value
    if (!el) return
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
  })
}

function stopTyping() {
  typing.value = false
  void nextTick(() => viewEl.value?.focus())
}

/** Looks a path up; null if it does not exist. */
async function stat(path: string): Promise<Entry | null> {
  try {
    return await fsApi.stat(path)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'not_found') return null
    throw e
  }
}

function finish(paths: string[], replace = false) {
  chooser.answer(ctx.windowId, { paths, replace }, folder.value)
}

function cancel() {
  chooser.answer(ctx.windowId, null)
}

/** A typed location: folders are opened, files chosen (open mode). */
async function acceptTyped() {
  const path = resolveTyped(typed.value, folder.value, ctx.home)
  const e = await stat(path)
  if (!e) {
    ctx.notify(`“${typed.value.trim()}” does not exist.`, 'error')
    return
  }
  if (e.type === 'dir') {
    navigate(e.path)
    stopTyping()
  } else if (mode === 'open') finish([e.path])
  else ctx.notify(`“${e.name}” is not a folder.`, 'error')
}

async function acceptSave() {
  const { dir, name: file } = splitTyped(name.value, folder.value, ctx.home)
  const lead = selectedEntries.value[0]
  if (!file) {
    // Nothing typed: Save with a folder selected opens it.
    if (!name.value.trim() && lead?.type === 'dir') navigate(lead.path)
    else if (name.value.trim()) navigate(dir)
    else ctx.notify(badName(file), 'error')
    return
  }
  const problem = badName(file)
  if (problem) {
    ctx.notify(problem, 'error')
    return
  }
  const path = joinPath(dir, file)
  const existing = await stat(path)
  if (existing?.type === 'dir') {
    navigate(existing.path)
    name.value = ''
    return
  }
  if (existing) {
    const ok = await ctx.confirm({
      title: `A file named “${file}” already exists. Do you want to replace it?`,
      message: `The file already exists in “${baseName(dir)}”. Replacing it will overwrite its contents.`,
      confirm: 'Replace',
      danger: true,
    })
    if (ok) finish([path], true)
    return
  }
  const parent = await stat(dir)
  if (parent?.type !== 'dir') {
    ctx.notify(`The folder “${dir}” does not exist.`, 'error')
    return
  }
  folder.value = dir
  finish([path])
}

/** The accepting button (Open, Save, Select). */
async function accept() {
  if (busy.value) return
  busy.value = true
  try {
    if (typing.value && typed.value.trim()) await acceptTyped()
    else if (mode === 'save') await acceptSave()
    else if (mode === 'folder') {
      const lead = selectedEntries.value[0]
      finish([lead?.type === 'dir' ? lead.path : folder.value])
    } else {
      const chosen = selectedEntries.value
      if (chosen.length === 1 && chosen[0]!.type === 'dir') navigate(chosen[0]!.path)
      else {
        const files = chosen.filter((e) => e.type !== 'dir').map((e) => e.path)
        if (files.length) finish(multiple ? files : files.slice(0, 1))
      }
    }
  } catch (e) {
    ctx.notify((e as Error).message, 'error')
  } finally {
    busy.value = false
  }
}

const canAccept = computed(() => {
  if (busy.value) return false
  if (typing.value && typed.value.trim()) return true
  if (mode === 'save') return !!name.value.trim() || selectedEntries.value[0]?.type === 'dir'
  if (mode === 'folder') return !error.value
  return selectedEntries.value.length > 0
})
const acceptLabel = computed(() => {
  const lead = selectedEntries.value
  if (mode === 'open' && lead.length === 1 && lead[0]!.type === 'dir' && !typing.value) return 'Open'
  return opts.accept ?? defaults.accept
})

async function createFolder() {
  const names = new Set(entries.value.map((e) => e.name))
  const answer = await ctx.prompt({
    title: 'Create Folder',
    label: 'Folder name',
    value: uniqueName('Untitled Folder', names),
    confirm: 'Create',
  })
  const n = answer?.trim()
  if (!n) return
  if (n.includes('/') || badName(n)) {
    ctx.notify(n.includes('/') ? 'Folder names cannot contain "/".' : badName(n), 'error')
    return
  }
  try {
    const e = await fsApi.mkdir(joinPath(folder.value, n))
    navigate(e.path)
  } catch (e) {
    ctx.notify(`Could not create the folder: ${(e as Error).message}`, 'error')
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    if (typing.value) stopTyping()
    else cancel()
  } else if (e.ctrlKey && e.key.toLowerCase() === 'h') {
    e.preventDefault()
    toggleHidden()
  } else if (e.ctrlKey && e.key.toLowerCase() === 'l') {
    e.preventDefault()
    startTyping(mode === 'save' ? '' : folder.value === '/' ? '/' : `${folder.value}/`)
  }
}

onMounted(async () => {
  void load()
  if (mode === 'save') {
    // Select the name without its extension, as GTK does.
    void nextTick(() => {
      const el = nameInput.value
      if (!el) return
      el.focus()
      const dot = name.value.lastIndexOf('.')
      el.setSelectionRange(0, dot > 0 ? dot : name.value.length)
    })
  } else void nextTick(() => viewEl.value?.focus())
  const [p, d] = await Promise.all([fsApi.places().catch(() => []), fsApi.drives().catch(() => null)])
  places.value = p
  volumes.value = d?.volumes ?? []
  await loadBookmarks()
})

onBeforeUnmount(() => {
  loadAbort?.abort()
  offFs()
  offBookmarks()
})
</script>

<template>
  <div class="chooser" @keydown="onKey">
    <div v-if="mode === 'save'" class="name-row">
      <label for="chooser-name">Name:</label>
      <input
        id="chooser-name"
        ref="nameInput"
        v-model="name"
        class="entry"
        autocomplete="off"
        spellcheck="false"
        @keydown.enter.prevent="accept"
      />
    </div>
    <div class="location-row">
      <button
        v-if="mode !== 'save'"
        class="btn icon-only"
        :class="{ on: typing }"
        :aria-pressed="typing"
        title="Type a file name (Ctrl+L)"
        aria-label="Type a file name"
        @click="typing ? stopTyping() : startTyping(folder === '/' ? '/' : `${folder}/`)"
      >
        <SymbolicIcon name="edit" />
      </button>
      <input
        v-if="typing"
        ref="locationInput"
        v-model="typed"
        class="entry location"
        aria-label="Location"
        autocomplete="off"
        spellcheck="false"
        @keydown.enter.prevent="accept"
      />
      <nav v-else class="pathbar" aria-label="Folder">
        <button
          v-for="(c, i) in crumbs"
          :key="c.path"
          class="btn crumb"
          :class="{ current: i === crumbs.length - 1 }"
          :title="c.path"
          :aria-label="c.label || 'File System'"
          :aria-current="i === crumbs.length - 1 ? 'location' : undefined"
          @click="navigate(c.path)"
        >
          <ThemeIcon v-if="c.icon" :name="c.icon" :size="16" />
          <span v-if="c.label">{{ c.label }}</span>
        </button>
      </nav>
      <button
        v-if="mode !== 'open'"
        class="btn icon-only"
        title="Create Folder"
        aria-label="Create Folder"
        @click="createFolder"
      >
        <SymbolicIcon name="folder-new" />
      </button>
    </div>

    <div class="body">
      <aside class="sidebar" aria-label="Places">
        <button
          v-for="p in sidebar"
          :key="p.path"
          class="place"
          :class="{ active: folder === p.path }"
          :title="p.path"
          @click="navigate(p.path)"
        >
          <ThemeIcon :name="p.icon" :size="16" />
          <span>{{ p.name }}</span>
        </button>
        <template v-if="devices.length">
          <hr />
          <button
            v-for="v in devices"
            :key="v.id"
            class="place"
            :class="{ active: folder === v.mountPoint }"
            :title="v.mountPoint"
            @click="navigate(v.mountPoint!)"
          >
            <ThemeIcon :name="v.icons" :size="16" />
            <span>{{ v.name }}</span>
          </button>
        </template>
        <template v-if="bookmarks.some((b) => b.exists)">
          <hr />
          <button
            v-for="b in bookmarks.filter((b) => b.exists)"
            :key="b.path"
            class="place"
            :class="{ active: folder === b.path }"
            :title="b.path"
            @click="navigate(b.path)"
          >
            <ThemeIcon :name="['folder']" :size="16" />
            <span>{{ b.name }}</span>
          </button>
        </template>
      </aside>

      <ContextMenuRoot>
        <ContextMenuTrigger as-child>
          <div ref="viewEl" class="view" tabindex="0" role="region" aria-label="Files" @keydown="onViewKey">
            <ItemsView
              ref="view"
              rename-value=""
              :items="items"
              mode="list"
              :icon-size="16"
              :selected="selected"
              :cut="new Set()"
              :drop-target="null"
              :renaming="null"
              :sort-key="sortKey"
              :sort-desc="sortDesc"
              date-label="Modified"
              :show-location="false"
              @item-click="click"
              @item-open="activate"
              @sort="setSort"
            />
            <p v-if="error" class="message">{{ error }}</p>
            <p v-else-if="!loading && !items.length" class="message dim">
              {{ entries.length ? 'No files match the filter.' : 'This folder is empty.' }}
            </p>
          </div>
        </ContextMenuTrigger>
        <ContextMenuPortal>
          <ContextMenuContent class="popup-menu">
            <ContextMenuItem class="item" @select="toggleHidden">
              {{ showHidden ? '✓ ' : '' }}Show Hidden Files
            </ContextMenuItem>
            <ContextMenuItem class="item" @select="startTyping(folder === '/' ? '/' : `${folder}/`)">
              Type a Location…
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenuPortal>
      </ContextMenuRoot>
    </div>

    <footer class="actions">
      <select v-if="filters.length > 1" v-model="filterIndex" class="entry filter" aria-label="Show files of type">
        <option v-for="(f, i) in filters" :key="i" :value="i">{{ f.name }}</option>
      </select>
      <span class="spacer" />
      <button class="btn" @click="cancel">Cancel</button>
      <button class="btn suggested" :disabled="!canAccept" @click="accept">{{ acceptLabel }}</button>
    </footer>
  </div>
</template>

<style scoped>
.chooser {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
}
.name-row,
.location-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px 0;
}
.name-row label {
  font-weight: bold;
}
.name-row .entry {
  flex: 1;
  max-width: 420px;
}
.location-row {
  padding-bottom: 8px;
}
.btn.on {
  background: var(--button-active-bg);
  box-shadow: inset 0 0 0 1px var(--accent);
}
.location {
  flex: 1;
}
.pathbar {
  flex: 1;
  display: flex;
  gap: 2px;
  min-width: 0;
  overflow: hidden;
}
.crumb {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: 200px;
  min-width: 0;
}
.crumb span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.crumb.current {
  font-weight: bold;
}
.body {
  flex: 1;
  display: flex;
  min-height: 0;
  margin: 0 10px;
  border: 1px solid var(--border);
}
.sidebar {
  width: 170px;
  flex: none;
  overflow-y: auto;
  padding: 4px 0;
  background: var(--sidebar-bg);
  color: var(--sidebar-fg);
  border-right: 1px solid var(--border);
}
.sidebar hr {
  margin: 4px 10px;
  border: none;
  border-top: 1px solid var(--border);
}
.place {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 5px 12px;
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
.view:focus-visible {
  box-shadow: inset 0 0 0 1px var(--accent);
}
.message {
  position: absolute;
  top: 40%;
  left: 0;
  right: 0;
  margin: 0;
  padding: 0 24px;
  text-align: center;
  pointer-events: none;
}
.actions {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px;
}
.spacer {
  flex: 1;
}
.actions .btn {
  min-width: 84px;
}
</style>
