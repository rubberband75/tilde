<script setup lang="ts">
// The text editor (D48, D63), after xed: a window of tabs, one document
// each (EditorTab). Files opened from Files, the desktop or Open… come in
// as tabs of the most recently used window (ctx.onLaunch); an empty tab
// nobody touched gives its place. Tabs drag between windows; the tab bar
// shows for a single document too, so it can be dragged into another.
import { computed, nextTick, reactive, ref, watch } from 'vue'
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from 'reka-ui'
import { draftsApi } from '@/api/text'
import { dirName, fsApi } from '@/api/fs'
import { isTextMime, textMimeTypes } from '@/apps/registry'
import { dragPaths, isPathDrag, isUploadDrag } from '@/lib/dnd'
import type { AppContext } from '@/apps/types'
import EditorTab, { type TabStatus } from './EditorTab.vue'
import RecoverPanel from './RecoverPanel.vue'
import { recoverable, shownDraftKeys, type Recoverable } from './recovery'
import { afterClose, moveTab, newTab, openPath, tabsFromArgs, type TabSpec, type Tabs } from './tabs'
import { endMove, moveFromElsewhere, startMove, TAB_DRAG, takeMove } from './transfer'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

const model = ref<Tabs>(tabsFromArgs(ctx.args))
const status = reactive<Record<string, TabStatus>>({})
const refs = new Map<string, InstanceType<typeof EditorTab>>()
const root = ref<HTMLElement | null>(null)

const tabs = computed(() => model.value.tabs)
const current = computed(() => tabs.value[model.value.active])
const now = computed(() => (current.value ? status[current.value.id] : undefined))
const active = () => (current.value ? refs.get(current.value.id) : undefined)

function nameOf(t: TabSpec) {
  return status[t.id]?.name ?? (t.path ? t.path.split('/').pop()! : 'Untitled Document')
}

const title = computed(() => {
  const s = now.value
  return `${s?.dirty ? '*' : ''}${s?.name ?? 'Untitled Document'} — Text Editor`
})
watch(title, (t) => ctx.setTitle(t), { immediate: true })

// The window keeps its tabs: a reload or a lock brings them back. Windows
// from before tabs kept one document as path, draft and wrap.
watch(
  model,
  (m) => ctx.saveState({ tabs: m.tabs, active: m.active, path: undefined, draft: undefined, wrap: undefined }),
  { deep: true, immediate: true },
)

function setModel(m: Tabs) {
  model.value = m
}

function update(t: TabSpec, patch: Partial<TabSpec>) {
  setModel({ ...model.value, tabs: tabs.value.map((x) => (x.id === t.id ? { ...x, ...patch } : x)) })
}

function select(i: number) {
  if (i >= 0 && i < tabs.value.length) setModel({ ...model.value, active: i })
}

function addTab() {
  const tabsNow = [...tabs.value]
  tabsNow.splice(model.value.active + 1, 0, newTab())
  setModel({ tabs: tabsNow, active: model.value.active + 1 })
}

const untouched = (t: TabSpec) => status[t.id]?.untouched ?? (!t.path && !status[t.id])

function open(path: string) {
  setModel(openPath(model.value, path, untouched))
}

/** An untitled draft brought back: in the active tab if untouched, else a new one. */
function openDraft(draft: string) {
  const spec: TabSpec = { id: crypto.randomUUID(), draft }
  const tabsNow = [...tabs.value]
  const replace = !!current.value && untouched(current.value)
  const at = replace ? model.value.active : model.value.active + 1
  tabsNow.splice(at, replace ? 1 : 0, spec)
  setModel({ tabs: tabsNow, active: at })
}

// Files opened elsewhere (Files, the desktop) come here when this window
// was the last one used.
ctx.onLaunch((args) => {
  if (typeof args.path === 'string') open(args.path)
})

function closeWindow() {
  ctx.setCloseGuard(null)
  ctx.close()
}

function remove(id: string) {
  delete status[id]
  refs.delete(id)
  const next = afterClose(model.value, id)
  if (next.tabs.length === 0) closeWindow()
  else setModel(next)
}

/** Closes a tab, asking first if it has unsaved changes, as xed does. */
async function closeTab(t: TabSpec) {
  if (status[t.id]?.dirty) {
    const ok = await ctx.confirm({
      title: `Close “${nameOf(t)}” without saving?`,
      message: 'Your changes will be lost.',
      confirm: 'Close Without Saving',
      danger: true,
    })
    if (!ok) return
    await refs.get(t.id)?.discard()
  }
  remove(t.id)
}

async function closeOthers(t: TabSpec) {
  for (const o of [...tabs.value]) if (o.id !== t.id) await closeTab(o)
}

/** A tab into a window of its own, unsaved changes and all (in its draft). */
async function moveToNewWindow(t: TabSpec) {
  await refs.get(t.id)?.flush()
  const args: Record<string, unknown> = t.path ? { path: t.path } : { draft: t.draft }
  if (t.wrap) args.wrap = true
  ctx.launch('text-editor', { ...args, newWindow: true })
  remove(t.id)
}

ctx.setCloseGuard(async () => {
  const dirty = tabs.value.filter((t) => status[t.id]?.dirty)
  if (dirty.length === 0) return true
  const ok = await ctx.confirm({
    title:
      dirty.length === 1
        ? `Close “${nameOf(dirty[0]!)}” without saving?`
        : `Close ${dirty.length} documents without saving?`,
    message:
      dirty.length === 1
        ? 'Your changes will be lost.'
        : `Changes to ${dirty.map((t) => `“${nameOf(t)}”`).join(', ')} will be lost.`,
    confirm: 'Close Without Saving',
    danger: true,
  })
  if (ok) for (const t of dirty) await refs.get(t.id)?.discard()
  return ok
})

const filters = [{ name: 'All Text Files', mimeTypes: textMimeTypes }, { name: 'All Files' }]

/** Opens files as tabs here; the last one chosen is shown. */
async function openFiles() {
  const folder = current.value?.path ? dirName(current.value.path) : undefined
  const chosen = await ctx.chooseFile({ mode: 'open', multiple: true, folder, filters })
  for (const path of chosen?.paths ?? []) open(path)
}

// Unsaved documents whose windows are gone (D58), offered while the window
// is one empty tab, and listed on request; they open as tabs here.
const recover = ref<Recoverable[]>([])
const recoverOpen = ref(false)
const recoverDismissed = ref(false)
const offerRecovery = computed(() => tabs.value.length === 1 && !!current.value && untouched(current.value))

async function loadRecoverable() {
  try {
    const drafts = await draftsApi.list()
    recover.value = recoverable(drafts, shownDraftKeys([...ctx.appWindows(), ctx.args]))
  } catch {
    recover.value = []
  }
}
watch(
  () => offerRecovery.value && !!now.value?.ready,
  (offer) => {
    if (offer) void loadRecoverable()
  },
  { immediate: true },
)

function openRecovered(item: Recoverable) {
  if ('path' in item.args) open(item.args.path)
  else openDraft(item.args.draft)
  recover.value = recover.value.filter((r) => r.key !== item.key)
  if (recover.value.length === 0) closeRecover()
}

async function discardRecovered(item: Recoverable) {
  const ok = await ctx.confirm({
    title: `Discard “${item.name}”?`,
    message: 'The unsaved text will be deleted. This cannot be undone.',
    confirm: 'Discard',
    danger: true,
  })
  if (!ok) return
  try {
    await draftsApi.remove(item.key)
  } catch (e) {
    ctx.notify(`Could not discard “${item.name}”: ${(e as Error).message}`, 'error')
  }
  await loadRecoverable()
}

function closeRecover() {
  recoverOpen.value = false
  void nextTick(() => active()?.focus())
}

/**
 * The window's own keys, anywhere in it. Not a CodeMirror keymap: that
 * reads Shift+S as a typed "S" and can take Ctrl+Shift+S for Ctrl+S. The
 * browser keeps Ctrl+N, Ctrl+W and Ctrl+Tab outside fullscreen (D53), so
 * Ctrl+Alt+N and Ctrl+Alt+W stand in for them.
 */
function onKey(e: KeyboardEvent) {
  const ctrl = e.ctrlKey || e.metaKey
  const key = e.key.toLowerCase()
  const step = (d: number) => select((model.value.active + d + tabs.value.length) % tabs.value.length)
  const handled = () => e.preventDefault()
  if (e.altKey && !ctrl && !e.shiftKey && /^[1-9]$/.test(e.key)) {
    handled()
    select(Number(e.key) - 1)
    return
  }
  if (!ctrl) return
  // Windows reports AltGr as Ctrl+Alt: AltGr+N or AltGr+W can type letters.
  if (e.altKey && e.getModifierState('AltGraph')) return
  if (e.code === 'PageDown' || e.code === 'PageUp') {
    handled()
    step(e.code === 'PageDown' ? 1 : -1)
  } else if (e.code === 'Tab') {
    handled()
    step(e.shiftKey ? -1 : 1)
  } else if (key === 'n' && !e.shiftKey) {
    handled()
    addTab()
  } else if (key === 'w' && !e.shiftKey) {
    handled()
    if (current.value) void closeTab(current.value)
  } else if (e.altKey) {
    return
  } else if (key === 's') {
    handled()
    void (e.shiftKey ? active()?.saveAs() : active()?.save())
  } else if (key === 'o' && !e.shiftKey) {
    handled()
    void openFiles()
  }
}

// Files dragged from Files or the desktop open as tabs, rather than
// CodeMirror pasting their paths: caught before it sees them.
const fileDrop = ref(false)

function onFileDragOver(e: DragEvent) {
  // A tab from another editor window: it would join this one (D63).
  if (e.dataTransfer?.types.includes(TAB_DRAG)) {
    if (!moveFromElsewhere(ctx.windowId)) return
    e.preventDefault()
    e.stopPropagation()
    e.dataTransfer.dropEffect = 'move'
    fileDrop.value = true
    // Over a tab: before or after it; elsewhere, at the end.
    const over = e.target instanceof Element ? e.target.closest<HTMLElement>('.tab') : null
    const all = [...(root.value?.querySelectorAll<HTMLElement>('.tab') ?? [])]
    const i = over ? all.indexOf(over) : -1
    if (over && i >= 0) {
      const box = over.getBoundingClientRect()
      dropAt.value = e.clientX < box.left + box.width / 2 ? i : i + 1
    } else dropAt.value = null
    return
  }
  if (!isPathDrag(e) && !isUploadDrag(e)) return
  e.preventDefault()
  e.stopPropagation()
  if (e.dataTransfer) e.dataTransfer.dropEffect = isPathDrag(e) ? 'copy' : 'none'
  fileDrop.value = isPathDrag(e)
}

function onFileDragLeave(e: DragEvent) {
  if (!root.value?.contains(e.relatedTarget as Node | null)) fileDrop.value = false
}

async function onFileDrop(e: DragEvent) {
  if (e.dataTransfer?.types.includes(TAB_DRAG)) {
    if (!moveFromElsewhere(ctx.windowId)) return
    e.preventDefault()
    e.stopPropagation()
    fileDrop.value = false
    const at = dropAt.value ?? tabs.value.length
    dropAt.value = null
    const spec = await takeMove(ctx.windowId)
    if (!spec) return
    // Here it is a document like any other: a new id, the same draft.
    const tabsNow = [...tabs.value]
    const existing = spec.path ? tabsNow.findIndex((t) => t.path === spec.path) : -1
    if (existing >= 0) {
      select(existing)
      return
    }
    tabsNow.splice(Math.min(at, tabsNow.length), 0, { ...spec, id: crypto.randomUUID() })
    setModel({ tabs: tabsNow, active: Math.min(at, tabsNow.length - 1) })
    return
  }
  if (!isPathDrag(e) && !isUploadDrag(e)) return
  e.preventDefault()
  e.stopPropagation()
  fileDrop.value = false
  if (!isPathDrag(e)) {
    // Not on this computer yet: a folder uploads them.
    ctx.notify('Files from your computer open once uploaded: drop them on a folder first.')
    return
  }
  const notText: string[] = []
  for (const path of dragPaths(e)) {
    const name = path.split('/').pop() || path
    try {
      const entry = await fsApi.stat(path)
      if (entry.type !== 'dir' && isTextMime(entry.mime)) open(path)
      else notText.push(name)
    } catch (err) {
      ctx.notify(`Could not open “${name}”: ${(err as Error).message}`, 'error')
    }
  }
  if (notText.length === 1) ctx.notify(`“${notText[0]}” is not a text file.`)
  else if (notText.length > 1) ctx.notify(`${notText.length} of the dropped items are not text files.`)
}

// Tabs reorder by dragging within the bar, and go to a window of their own
// when dropped outside this one.
const dragging = ref<string | null>(null)
const dropAt = ref<number | null>(null)

function onDragStart(t: TabSpec, e: DragEvent) {
  dragging.value = t.id
  e.dataTransfer?.setData(TAB_DRAG, t.id)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
  // Another window may take it: its unsaved changes go to the draft now.
  startMove(ctx.windowId, t, refs.get(t.id)?.flush() ?? Promise.resolve())
}

/** Over a tab: where a dragged tab would go, from this window or another. */
function onDragOver(i: number, e: DragEvent) {
  if (!e.dataTransfer?.types.includes(TAB_DRAG)) return
  if (!dragging.value && !moveFromElsewhere(ctx.windowId)) return
  e.preventDefault()
  e.dataTransfer.dropEffect = 'move'
  const box = (e.currentTarget as HTMLElement).getBoundingClientRect()
  dropAt.value = e.clientX < box.left + box.width / 2 ? i : i + 1
}

function onDrop(e: DragEvent) {
  // From another window: the window's own drop handler takes it.
  if (!dragging.value) return
  const from = tabs.value.findIndex((t) => t.id === dragging.value)
  if (from >= 0 && dropAt.value !== null) {
    e.preventDefault()
    setModel(moveTab(model.value, from, dropAt.value))
  }
  dragging.value = null
  dropAt.value = null
}

function onDragEnd(t: TabSpec, e: DragEvent) {
  const box = root.value?.getBoundingClientRect()
  const outside =
    !!box && (e.clientX < box.left || e.clientX > box.right || e.clientY < box.top || e.clientY > box.bottom)
  const mine = dragging.value === t.id
  dragging.value = null
  dropAt.value = null
  if (endMove()) {
    // Another editor window took it, unsaved changes and all (its draft).
    remove(t.id)
  } else if (mine && e.dataTransfer?.dropEffect === 'none' && outside && tabs.value.length > 1) {
    void moveToNewWindow(t)
  }
}
</script>

<template>
  <div
    ref="root"
    class="editor-app"
    :class="{ fileDrop }"
    @keydown="onKey"
    @dragover.capture="onFileDragOver"
    @dragleave="onFileDragLeave"
    @drop.capture="onFileDrop"
  >
    <header class="toolbar">
      <button class="btn" title="New document (Ctrl+Alt+N)" @click="addTab">New</button>
      <button class="btn" title="Open (Ctrl+O)" @click="openFiles">Open…</button>
      <button class="btn" title="Save (Ctrl+S)" :disabled="!now?.ready || now?.saving" @click="active()?.save()">
        Save
      </button>
      <button
        class="btn"
        title="Save As (Ctrl+Shift+S)"
        :disabled="!now?.ready || now?.saving"
        @click="active()?.saveAs()"
      >
        Save As…
      </button>
      <span class="sep" />
      <button class="btn" title="Undo (Ctrl+Z)" :disabled="!now?.ready" @click="active()?.command('undo')">Undo</button>
      <button class="btn" title="Redo (Ctrl+Shift+Z)" :disabled="!now?.ready" @click="active()?.command('redo')">
        Redo
      </button>
      <span class="sep" />
      <button class="btn" title="Find and replace (Ctrl+F)" :disabled="!now?.ready" @click="active()?.command('find')">
        Find
      </button>
      <button
        class="btn"
        :class="{ on: now?.wrap }"
        :aria-pressed="!!now?.wrap"
        title="Wrap long lines"
        @click="active()?.toggleWrap()"
      >
        Wrap Lines
      </button>
    </header>
    <nav class="tabbar" role="tablist" aria-label="Documents" @drop="onDrop" @dragover.prevent>
      <ContextMenuRoot v-for="(t, i) in tabs" :key="t.id" :modal="false">
        <ContextMenuTrigger as-child>
          <div
            class="tab"
            role="tab"
            :aria-selected="i === model.active"
            :class="{
              on: i === model.active,
              dropBefore: dropAt === i,
              dropAfter: dropAt === i + 1 && i === tabs.length - 1,
            }"
            :title="t.path ?? 'Never saved'"
            draggable="true"
            @click="select(i)"
            @auxclick.middle="closeTab(t)"
            @dragstart="onDragStart(t, $event)"
            @dragover="onDragOver(i, $event)"
            @dragend="onDragEnd(t, $event)"
          >
            <span class="name">{{ nameOf(t) }}</span>
            <span v-if="status[t.id]?.dirty" class="dirty" aria-label="unsaved">●</span>
            <button
              class="close"
              :aria-label="`Close ${nameOf(t)}`"
              title="Close (Ctrl+Alt+W)"
              @click.stop="closeTab(t)"
            >
              ×
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuPortal>
          <ContextMenuContent class="popup-menu">
            <ContextMenuItem class="item" @select="moveToNewWindow(t)">Move to New Window</ContextMenuItem>
            <ContextMenuSeparator class="separator" />
            <ContextMenuItem class="item" @select="closeOthers(t)">Close Other Tabs</ContextMenuItem>
            <ContextMenuItem class="item" @select="closeTab(t)">Close</ContextMenuItem>
          </ContextMenuContent>
        </ContextMenuPortal>
      </ContextMenuRoot>
    </nav>
    <div v-if="offerRecovery && recover.length && !recoverOpen && !recoverDismissed" class="infobar" role="status">
      <span>
        {{ recover.length === 1 ? '1 unsaved document' : `${recover.length} unsaved documents` }} can be recovered.
      </span>
      <button class="btn" @click="recoverOpen = true">Show</button>
      <button class="btn" @click="recoverDismissed = true">Not Now</button>
    </div>
    <div class="body">
      <EditorTab
        v-for="(t, i) in tabs"
        v-show="i === model.active"
        :key="t.id"
        :ref="(el) => (el ? refs.set(t.id, el as InstanceType<typeof EditorTab>) : refs.delete(t.id))"
        class="pane"
        :ctx="ctx"
        :tab="t"
        :active="i === model.active"
        @update="(patch) => update(t, patch)"
        @status="(s) => (status[t.id] = s)"
        @failed="remove(t.id)"
      />
      <RecoverPanel
        v-if="recoverOpen"
        :items="recover"
        @open="openRecovered"
        @discard="discardRecovered"
        @close="closeRecover"
      />
    </div>
  </div>
</template>

<style scoped>
.editor-app {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
}
.toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px;
  background: var(--headerbar-bg);
  border-bottom: 1px solid var(--border);
}
.toolbar .btn {
  height: 28px;
}
.toolbar .btn.on {
  background: var(--button-active-bg);
  box-shadow: inset 0 0 0 1px var(--accent);
}
.sep {
  width: 1px;
  height: 20px;
  margin: 0 4px;
  background: var(--border);
}
.tabbar {
  display: flex;
  overflow-x: auto;
  background: var(--headerbar-bg);
  border-bottom: 1px solid var(--border);
}
.tab {
  display: flex;
  flex: 0 1 200px;
  align-items: center;
  gap: 6px;
  min-width: 90px;
  padding: 4px 4px 4px 12px;
  border-right: 1px solid var(--border);
  border-bottom: 2px solid transparent;
  color: var(--view-dim-fg);
  cursor: default;
}
.tab:hover {
  background: var(--hover-bg);
}
.tab.on {
  border-bottom-color: var(--accent);
  background: var(--window-bg);
  color: var(--window-fg);
}
.tab.dropBefore {
  box-shadow: inset 2px 0 0 var(--accent);
}
.tab.dropAfter {
  box-shadow: inset -2px 0 0 var(--accent);
}
.name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.dirty {
  font-size: 0.7em;
}
.close {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: inherit;
  line-height: 1;
}
.close:hover {
  background: var(--button-hover-bg);
}
.infobar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: var(--editor-infobar-bg);
  border-bottom: 1px solid var(--border);
}
.infobar span {
  flex: 1;
}
.body {
  position: relative;
  flex: 1;
  min-height: 0;
}
/* Files or a tab dragged over: they would open here. Drawn above the
   editor and its status bar, which would cover an outline of their own. */
.editor-app {
  position: relative;
}
.editor-app.fileDrop::after {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 10;
  border: 2px solid var(--accent);
  pointer-events: none;
}
.pane {
  height: 100%;
}
</style>
