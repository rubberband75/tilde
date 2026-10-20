<script setup lang="ts">
// Desktop icons: Computer, Home and Trash, then the contents of the user's
// Desktop folder (from ~/.config/user-dirs.dirs). Icons fill columns top to
// bottom, like Nemo's desktop.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ContextMenuContent, ContextMenuItem, ContextMenuPortal, ContextMenuRoot, ContextMenuSeparator, ContextMenuTrigger } from 'reka-ui'
import { fsApi, joinPath, uniqueName } from '@/api/fs'
import type { Entry } from '@/api/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { emitFsChanged, onFsChanged } from '@/lib/bus'
import { openEntry } from '@/shell/actions'
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

async function load() {
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

const selectedEntries = computed(() => items.value.filter((i) => i.entry && selected.value.has(i.key)).map((i) => i.entry!))

async function trashSelected() {
  const paths = selectedEntries.value.map((e) => e.path)
  if (!paths.length) return
  const results = await fsApi.trash(paths)
  for (const r of results) if (r.error) notices.push(`Could not move ${r.path} to the trash: ${r.error}`, 'error')
  emitFsChanged(desktopDir.value, trashFilesDir.value)
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
  if (e.key === 'Delete') void trashSelected()
  if (e.key === 'Enter') items.value.filter((i) => selected.value.has(i.key)).forEach((i) => i.open())
}

async function onDrop(e: DragEvent) {
  dropActive.value = false
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

let poll: ReturnType<typeof setInterval> | undefined
let offFs: (() => void) | undefined
onMounted(() => {
  void load()
  // TODO(phase 4): replace polling with inotify events from the worker.
  poll = setInterval(() => document.visibilityState === 'visible' && load(), 10_000)
  offFs = onFsChanged((dir) => {
    if (dir === desktopDir.value || dir === trashFilesDir.value) void load()
  })
})
onBeforeUnmount(() => {
  clearInterval(poll)
  offFs?.()
})
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
        @dragover.prevent="dropActive = true"
        @dragleave.self="dropActive = false"
        @drop.prevent="onDrop"
      >
        <button
          v-for="item in items"
          :key="item.key"
          class="desktop-icon"
          :class="{ selected: selected.has(item.key) }"
          :title="item.name"
          @click="select(item, $event)"
          @dblclick="item.open()"
          @contextmenu="onItemContext(item)"
        >
          <ThemeIcon :name="item.icon" :size="48" />
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
            <ContextMenuItem class="item" @select="trashSelected">Move to Trash</ContextMenuItem>
          </template>
        </template>
        <template v-else>
          <ContextMenuItem class="item" @select="newFolder">New Folder</ContextMenuItem>
          <ContextMenuItem class="item" @select="wm.open('terminal', { cwd: desktopDir })">Open in Terminal</ContextMenuItem>
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
