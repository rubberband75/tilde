<script setup lang="ts">
// One tab of the text editor (D48, D63): one document, its CodeMirror and
// its status bar. Unsaved changes go to the worker as a draft while the
// user types (document.ts), so a lock, a reload or another browser finds
// them; the file is only written on Save. The window (EditorApp) holds the
// tabs and the toolbar, which works on the active one.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch, watchEffect } from 'vue'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { redo, undo } from '@codemirror/commands'
import { openSearchPanel } from '@codemirror/search'
import { ApiError } from '@/api/client'
import { dirName } from '@/api/fs'
import type { AppContext } from '@/apps/types'
import { onBeforeLock } from '@/lib/beforeLock'
import { onFsChanged } from '@/lib/bus'
import { useWatchedDirs } from '@/lib/fswatch'
import { EditorDocument } from './document'
import { baseExtensions, languageFor } from './setup'
import type { TabSpec } from './tabs'

const props = defineProps<{ ctx: AppContext; tab: TabSpec; active: boolean }>()
const emit = defineEmits<{
  /** The tab's saved state changed: a file after Save As, Wrap Lines. */
  update: [patch: Partial<TabSpec>]
  /** What the tab bar and the toolbar show. */
  status: [status: TabStatus]
  /** The document could not be opened: the tab goes. */
  failed: []
}>()

export interface TabStatus {
  name: string
  path?: string
  dirty: boolean
  ready: boolean
  saving: boolean
  wrap: boolean
  /** An empty new document nobody has typed in: it can give its place away. */
  untouched: boolean
}

const ctx = props.ctx
const host = ref<HTMLElement | null>(null)
const view = shallowRef<EditorView | null>(null)
const ready = ref(false)
const saving = ref(false)
const languageName = ref('Plain Text')
const cursor = ref({ line: 1, col: 1 })
const wrap = ref(props.tab.wrap === true)
/** Whether the text is empty, for "untouched" (the view's text is not reactive). */
const empty = ref(true)

const doc = new EditorDocument(props.tab.path ?? '', props.tab.draft)
const state = doc.state

const language = new Compartment()
const wrapping = new Compartment()

const untouched = computed(() => !state.path && !state.dirty && empty.value)
watchEffect(() =>
  emit('status', {
    name: doc.name,
    path: state.path || undefined,
    dirty: state.dirty,
    ready: ready.value,
    saving: saving.value,
    wrap: wrap.value,
    untouched: untouched.value,
  }),
)

function text(): string {
  return view.value?.state.doc.toString() ?? ''
}

// Set while the text is replaced from disk, which is not the user's edit.
let replacing = false

function setText(value: string) {
  const v = view.value
  if (!v) return
  const head = Math.min(v.state.selection.main.head, value.length)
  replacing = true
  try {
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value }, selection: { anchor: head } })
  } finally {
    replacing = false
  }
}

async function setLanguage() {
  const desc = state.path ? languageFor(state.path.split('/').pop()!) : null
  languageName.value = desc?.name ?? 'Plain Text'
  const support = desc ? await desc.load().catch(() => null) : null
  view.value?.dispatch({ effects: language.reconfigure(support ?? []) })
}

async function save(): Promise<boolean> {
  if (saving.value) return false
  if (!state.path) return saveAs()
  saving.value = true
  try {
    let result = await doc.save(text())
    if (result === 'changed') {
      const ok = await ctx.confirm({
        title: state.missing ? `“${doc.name}” was deleted or moved` : `“${doc.name}” changed on disk`,
        message: state.missing ? 'Save it again where it was?' : 'Saving replaces the changes made on disk.',
        confirm: 'Save Anyway',
        danger: !state.missing,
      })
      if (!ok) return false
      result = await doc.save(text(), true)
    }
    return result === 'saved'
  } catch (e) {
    ctx.notify(`Could not save “${doc.name}”: ${(e as Error).message}`, 'error')
    return false
  } finally {
    saving.value = false
  }
}

/** The folder the choosers start in: the document's, else the last one used. */
function chooserFolder(): string | undefined {
  return state.path ? dirName(state.path) : undefined
}

async function saveAs(): Promise<boolean> {
  if (saving.value) return false
  const chosen = await ctx.chooseFile({
    mode: 'save',
    title: 'Save As',
    folder: chooserFolder(),
    name: state.path ? doc.name : 'Untitled Document.txt',
  })
  const path = chosen?.paths[0]
  if (!path) return false
  saving.value = true
  try {
    let result = await doc.saveAs(path, text(), chosen.replace)
    // Created meanwhile, after the chooser looked.
    if (result === 'exists') {
      const ok = await ctx.confirm({
        title: `Replace “${path.split('/').pop()}”?`,
        message: `A file with this name already exists in ${dirName(path)}.`,
        confirm: 'Replace',
        danger: true,
      })
      if (!ok) return false
      result = await doc.saveAs(path, text(), true)
    }
    if (result !== 'saved') return false
    emit('update', { path })
    await setLanguage()
    return true
  } catch (e) {
    ctx.notify(`Could not save “${path}”: ${(e as Error).message}`, 'error')
    return false
  } finally {
    saving.value = false
  }
}

async function reload() {
  try {
    setText(await doc.reload())
  } catch (e) {
    ctx.notify(`Could not reload “${doc.name}”: ${(e as Error).message}`, 'error')
  }
}

function toggleWrap() {
  wrap.value = !wrap.value
  emit('update', { wrap: wrap.value || undefined })
  view.value?.dispatch({ effects: wrapping.reconfigure(wrap.value ? EditorView.lineWrapping : []) })
}

function command(name: 'undo' | 'redo' | 'find') {
  const v = view.value
  if (!v) return
  ;({ undo, redo, find: openSearchPanel })[name](v)
  v.focus()
}

function focus() {
  view.value?.focus()
}

// The file's folder: changes on disk reach an unmodified document at once,
// and a modified one is told. Not during a save, which changes it too.
useWatchedDirs(() => (state.path ? [dirName(state.path)] : []))
const offFs = onFsChanged((dir) => {
  if (!ready.value || saving.value || !state.path || dir !== dirName(state.path)) return
  void doc.checkDisk().then((fresh) => {
    if (fresh !== null && !saving.value) setText(fresh)
  })
})
const offLock = onBeforeLock(() => doc.flush())

// Shown again: CodeMirror measures the space it now has, and takes the focus.
watch(
  () => props.active,
  async (on) => {
    if (!on) return
    await nextTick()
    view.value?.requestMeasure()
    view.value?.focus()
  },
)

onMounted(async () => {
  let initial: string
  try {
    initial = await doc.open()
  } catch (e) {
    const reason = e instanceof ApiError ? e.message : 'Could not reach the session.'
    ctx.notify(`Could not open “${doc.name}”: ${reason}`, 'error')
    emit('failed')
    return
  }
  empty.value = initial === ''
  view.value = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: initial,
      extensions: [
        ...baseExtensions(),
        language.of([]),
        wrapping.of(wrap.value ? EditorView.lineWrapping : []),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) empty.value = u.state.doc.length === 0
          if (u.docChanged && !replacing) doc.edited(() => view.value!.state.doc.toString())
          if (u.docChanged || u.selectionSet) {
            const head = u.state.selection.main.head
            const line = u.state.doc.lineAt(head)
            cursor.value = { line: line.number, col: head - line.from + 1 }
          }
        }),
      ],
    }),
  })
  ready.value = true
  if (props.active) view.value.focus()
  await setLanguage()
})

onBeforeUnmount(() => {
  offFs()
  offLock()
  // A reload, a lock or a tab moving away: keep what was typed since the
  // last draft. A tab closed without saving has discarded its draft first.
  void doc.flush().catch(() => {})
  view.value?.destroy()
})

defineExpose({
  save,
  saveAs,
  command,
  toggleWrap,
  focus,
  /** Writes the pending draft now: before the tab moves to another window. */
  flush: () => doc.flush(),
  /** Throws the unsaved changes away: closing without saving. */
  discard: () => doc.discard(),
})
</script>

<template>
  <div class="editor-tab">
    <div v-if="state.changedOnDisk" class="infobar" role="status">
      <span>“{{ doc.name }}” changed on disk after your unsaved changes.</span>
      <button class="btn" @click="reload">Reload</button>
      <button class="btn" @click="state.changedOnDisk = false">Keep My Changes</button>
    </div>
    <div v-else-if="state.missing" class="infobar" role="status">
      <span>“{{ doc.name }}” was deleted or moved. Save to write it again.</span>
    </div>
    <div v-else-if="ready && !state.writable" class="infobar" role="status">
      <span>You do not have permission to change “{{ doc.name }}”.</span>
      <button class="btn" @click="saveAs">Save As…</button>
    </div>
    <div ref="host" class="host" />
    <footer class="status">
      <span>{{ languageName }}</span>
      <span>Ln {{ cursor.line }}, Col {{ cursor.col }}</span>
      <span>UTF-8</span>
    </footer>
  </div>
</template>

<style scoped>
.editor-tab {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
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
.host {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.status {
  display: flex;
  justify-content: flex-end;
  gap: 24px;
  padding: 3px 12px;
  background: var(--headerbar-bg);
  color: var(--view-dim-fg);
  border-top: 1px solid var(--border);
  font-size: 0.9em;
}
</style>
