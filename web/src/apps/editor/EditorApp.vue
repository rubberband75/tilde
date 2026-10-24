<script setup lang="ts">
// The text editor (D48), after xed: one document per window, opened from
// Files or started empty from the menu. Unsaved changes go to the worker
// as a draft while the user types (document.ts), so a lock, a reload or
// another browser finds them; the file is only written on Save.
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { redo, undo } from '@codemirror/commands'
import { openSearchPanel } from '@codemirror/search'
import { ApiError } from '@/api/client'
import { dirName, joinPath } from '@/api/fs'
import type { AppContext } from '@/apps/types'
import { onBeforeLock } from '@/lib/beforeLock'
import { onFsChanged } from '@/lib/bus'
import { useWatchedDirs } from '@/lib/fswatch'
import { EditorDocument } from './document'
import { baseExtensions, languageFor } from './setup'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

const host = ref<HTMLElement | null>(null)
const view = shallowRef<EditorView | null>(null)
const ready = ref(false)
const saving = ref(false)
const languageName = ref('Plain Text')
const cursor = ref({ line: 1, col: 1 })
const wrap = ref(ctx.args.wrap === true)

let draftId = typeof ctx.args.draft === 'string' ? ctx.args.draft : ''
if (!draftId) {
  draftId = crypto.randomUUID()
  ctx.saveState({ draft: draftId })
}
const doc = new EditorDocument(typeof ctx.args.path === 'string' ? ctx.args.path : '', draftId)
const state = doc.state

const language = new Compartment()
const wrapping = new Compartment()

const title = computed(() => `${state.dirty ? '*' : ''}${doc.name} — Text Editor`)
watch(title, (t) => ctx.setTitle(t), { immediate: true })

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

async function saveAs(): Promise<boolean> {
  const suggested = state.path || joinPath(ctx.home, 'Untitled Document.txt')
  const answer = await ctx.prompt({
    title: 'Save As',
    message: 'The full path of the file; its folder must exist.',
    label: 'File path',
    value: suggested,
    confirm: 'Save',
  })
  if (!answer?.trim()) return false
  let path = answer.trim()
  if (path === '~' || path.startsWith('~/')) path = ctx.home + path.slice(1)
  else if (!path.startsWith('/')) path = joinPath(ctx.home, path)
  saving.value = true
  try {
    let result = await doc.saveAs(path, text())
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
    ctx.saveState({ path })
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
  ctx.saveState({ wrap: wrap.value })
  view.value?.dispatch({ effects: wrapping.reconfigure(wrap.value ? EditorView.lineWrapping : []) })
}

function command(fn: (v: EditorView) => boolean) {
  const v = view.value
  if (!v) return
  fn(v)
  v.focus()
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

ctx.setCloseGuard(async () => {
  if (!state.dirty) return true
  const ok = await ctx.confirm({
    title: `Close “${doc.name}” without saving?`,
    message: 'Your changes will be lost.',
    confirm: 'Close Without Saving',
    danger: true,
  })
  if (ok) await doc.discard()
  return ok
})

onMounted(async () => {
  let initial: string
  try {
    initial = await doc.open()
  } catch (e) {
    const reason = e instanceof ApiError ? e.message : 'Could not reach the session.'
    ctx.notify(`Could not open “${doc.name}”: ${reason}`, 'error')
    ctx.setCloseGuard(null)
    ctx.close()
    return
  }
  view.value = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: initial,
      extensions: [
        keymap.of([
          { key: 'Mod-s', preventDefault: true, run: () => (void save(), true) },
          { key: 'Shift-Mod-s', preventDefault: true, run: () => (void saveAs(), true) },
        ]),
        ...baseExtensions(),
        language.of([]),
        wrapping.of(wrap.value ? EditorView.lineWrapping : []),
        EditorView.updateListener.of((u) => {
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
  view.value.focus()
  await setLanguage()
})

onBeforeUnmount(() => {
  offFs()
  offLock()
  // A reload or a lock: keep what was typed since the last draft.
  void doc.flush().catch(() => {})
  view.value?.destroy()
})
</script>

<template>
  <div class="editor-app">
    <header class="toolbar">
      <button class="btn" title="New document" @click="ctx.launch('text-editor')">New</button>
      <button class="btn" title="Save (Ctrl+S)" :disabled="!ready || saving" @click="save">Save</button>
      <button class="btn" title="Save As (Ctrl+Shift+S)" :disabled="!ready || saving" @click="saveAs">Save As…</button>
      <span class="sep" />
      <button class="btn" title="Undo (Ctrl+Z)" :disabled="!ready" @click="command(undo)">Undo</button>
      <button class="btn" title="Redo (Ctrl+Shift+Z)" :disabled="!ready" @click="command(redo)">Redo</button>
      <span class="sep" />
      <button class="btn" title="Find and replace (Ctrl+F)" :disabled="!ready" @click="command(openSearchPanel)">
        Find
      </button>
      <button class="btn" :class="{ on: wrap }" :aria-pressed="wrap" title="Wrap long lines" @click="toggleWrap">
        Wrap Lines
      </button>
    </header>
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
