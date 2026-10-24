<script setup lang="ts">
// The image viewer, after xviewer: one image per window, opened from Files
// or with Open; the folder's other images one key away; Best Fit or a
// zoom (wheel, keys, toolbar) with the picture dragged around when it is
// bigger than the window; turning it a quarter at a time (only the view);
// Move to Trash; fullscreen and a slideshow. The browser draws the image
// from the worker's fs/content, which serves it with a sandbox CSP.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { ApiError } from '@/api/client'
import { baseName, dirName, fsApi } from '@/api/fs'
import type { Entry } from '@/api/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { onFsChanged } from '@/lib/bus'
import { formatSize } from '@/lib/format'
import { useWatchedDirs } from '@/lib/fswatch'
import { clampZoom, fitZoom, folderImages, neighbour, stepZoom, viewableImageTypes } from './viewer'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

const SLIDE_MS = 5000

const path = ref(typeof ctx.args.path === 'string' ? ctx.args.path : '')
const entry = shallowRef<Entry | null>(null)
const images = shallowRef<Entry[]>([])
const natural = ref({ w: 0, h: 0 })
const loaded = ref(false)
const error = ref('')
const fit = ref(true)
const zoom = ref(1)
const turns = ref(0)
const fullscreen = ref(false)
const slideshow = ref(false)

const root = ref<HTMLElement | null>(null)
const stage = ref<HTMLElement | null>(null)
const stageSize = ref({ w: 0, h: 0 })

const name = computed(() => (path.value ? baseName(path.value) : ''))
watch(name, (n) => ctx.setTitle(n ? `${n} — Image Viewer` : 'Image Viewer'), { immediate: true })

const url = computed(() => (entry.value ? fsApi.contentUrl(entry.value.path, false, entry.value.mtime) : ''))
const index = computed(() => images.value.findIndex((e) => e.path === path.value))

// Geometry: the image at zoom, turned; the canvas is at least the stage,
// so a small image sits in the middle and a big one scrolls.
const turned = computed(() => turns.value % 2 !== 0)
const shown = computed(() => ({ w: natural.value.w * zoom.value, h: natural.value.h * zoom.value }))
const box = computed(() =>
  turned.value ? { w: shown.value.h, h: shown.value.w } : { w: shown.value.w, h: shown.value.h },
)
const pannable = computed(() => box.value.w > stageSize.value.w + 1 || box.value.h > stageSize.value.h + 1)

function applyFit() {
  if (fit.value && loaded.value) zoom.value = fitZoom(natural.value, stageSize.value, turns.value)
}
watch([stageSize, natural, turns, fit], applyFit)

/** Shows path: the folder's entry for it if listed, else a stat. */
async function show(p: string) {
  if (p === path.value && entry.value?.path === p && loaded.value) return
  path.value = p
  ctx.saveState({ path: p })
  loaded.value = false
  error.value = ''
  turns.value = 0
  fit.value = true
  const listed = images.value.find((e) => e.path === p)
  if (listed) {
    entry.value = listed
    return
  }
  try {
    const e = await fsApi.stat(p)
    if (path.value !== p) return
    entry.value = e
  } catch (e) {
    if (path.value !== p) return
    entry.value = null
    error.value =
      e instanceof ApiError && e.code === 'not_found'
        ? `“${baseName(p)}” does not exist.`
        : `Could not open “${baseName(p)}”: ${(e as Error).message}`
  }
}

let listAbort: AbortController | undefined

/** Lists the folder's images; follows the shown one if it was replaced or removed. */
async function loadFolder() {
  const dir = path.value ? dirName(path.value) : ''
  if (!dir) return
  listAbort?.abort()
  const ac = new AbortController()
  listAbort = ac
  const acc: Entry[] = []
  try {
    await fsApi.listStream(dir, (batch) => acc.push(...batch), ac.signal)
  } catch {
    return // The viewer still shows the one image.
  }
  if (ac.signal.aborted || dirName(path.value) !== dir) return
  const before = images.value
  images.value = folderImages(acc, path.value)
  const now = images.value.find((e) => e.path === path.value)
  if (now) {
    if (now.mtime !== entry.value?.mtime || now.size !== entry.value?.size) entry.value = now
  } else if (entry.value && before.some((e) => e.path === path.value)) {
    // Deleted or moved away: on to the image that took its place.
    const i = before.findIndex((e) => e.path === path.value)
    const next = images.value[Math.min(i, images.value.length - 1)]
    if (next) void show(next.path)
    else {
      entry.value = null
      error.value = `“${name.value}” was deleted or moved.`
    }
  }
}

const folder = computed(() => (path.value ? dirName(path.value) : ''))
watch(folder, () => void loadFolder())
useWatchedDirs(() => [folder.value])
const offFs = onFsChanged((dir) => {
  if (dir === folder.value) void loadFolder()
})

function go(dir: 1 | -1) {
  const next = neighbour(images.value, path.value, dir)
  if (next && next.path !== path.value) void show(next.path)
}

function goTo(i: number) {
  const e = images.value[i]
  if (e && e.path !== path.value) void show(e.path)
}

// The next image is fetched ahead, so stepping through a folder is quick.
let preload: HTMLImageElement | undefined
watch(loaded, (ok) => {
  if (!ok) return
  const next = neighbour(images.value, path.value, 1)
  if (next && next.path !== path.value) {
    preload = new Image()
    preload.src = fsApi.contentUrl(next.path, false, next.mtime)
  }
})

function onLoad(e: Event) {
  const img = e.target as HTMLImageElement
  // An SVG without a size of its own reports 0; give it one to fit.
  natural.value = { w: img.naturalWidth || 512, h: img.naturalHeight || 512 }
  loaded.value = true
  error.value = ''
  applyFit()
}

function onError() {
  if (!entry.value) return
  loaded.value = false
  error.value = `Could not show “${name.value}”: the file is damaged or not an image this browser can draw.`
}

/**
 * Sets the zoom, keeping the image point under (ax, ay) — the pointer, or
 * the middle of the view — where it is.
 */
async function setZoom(z: number, ax?: number, ay?: number) {
  const st = stage.value
  if (!st || !loaded.value) return
  z = clampZoom(z)
  fit.value = false
  const x = ax ?? st.clientWidth / 2
  const y = ay ?? st.clientHeight / 2
  const canvasW = Math.max(box.value.w, st.clientWidth)
  const canvasH = Math.max(box.value.h, st.clientHeight)
  const px = (st.scrollLeft + x - canvasW / 2) / zoom.value
  const py = (st.scrollTop + y - canvasH / 2) / zoom.value
  zoom.value = z
  await nextTick()
  st.scrollLeft = px * z + Math.max(box.value.w, st.clientWidth) / 2 - x
  st.scrollTop = py * z + Math.max(box.value.h, st.clientHeight) / 2 - y
}

function zoomStep(dir: 1 | -1) {
  void setZoom(stepZoom(zoom.value, dir))
}

function bestFit() {
  fit.value = true
  applyFit()
}

function onWheel(e: WheelEvent) {
  if (!loaded.value) return
  e.preventDefault()
  const rect = stage.value!.getBoundingClientRect()
  const delta = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY
  void setZoom(zoom.value * Math.exp(-delta * 0.0025), e.clientX - rect.left, e.clientY - rect.top)
}

const dragging = ref(false)

/** Dragging moves a picture bigger than the window. */
function onPointerDown(e: PointerEvent) {
  if (e.button !== 0 || !pannable.value) return
  const st = stage.value!
  st.setPointerCapture(e.pointerId)
  dragging.value = true
  let lx = e.clientX
  let ly = e.clientY
  const move = (ev: PointerEvent) => {
    st.scrollLeft -= ev.clientX - lx
    st.scrollTop -= ev.clientY - ly
    lx = ev.clientX
    ly = ev.clientY
  }
  const up = () => {
    dragging.value = false
    st.removeEventListener('pointermove', move)
    st.removeEventListener('pointerup', up)
    st.removeEventListener('pointercancel', up)
  }
  st.addEventListener('pointermove', move)
  st.addEventListener('pointerup', up)
  st.addEventListener('pointercancel', up)
}

function rotate(dir: 1 | -1) {
  turns.value = (turns.value + dir + 4) % 4
}

async function trash() {
  const e = entry.value
  if (!e) return
  try {
    const [result] = await fsApi.trash([e.path])
    if (result?.error) throw new Error(result.error)
  } catch (err) {
    ctx.notify(`Could not move “${e.name}” to the trash: ${(err as Error).message}`, 'error')
    return
  }
  const i = images.value.findIndex((x) => x.path === e.path)
  const rest = images.value.filter((x) => x.path !== e.path)
  images.value = rest
  const next = rest[Math.min(Math.max(i, 0), rest.length - 1)]
  if (next) void show(next.path)
  else {
    entry.value = null
    path.value = ''
    ctx.saveState({ path: '' })
  }
}

async function openImages() {
  const chosen = await ctx.chooseFile({
    mode: 'open',
    title: 'Open Image',
    folder: folder.value || undefined,
    multiple: true,
    filters: [{ name: 'All Images', mimeTypes: viewableImageTypes }, { name: 'All Files' }],
  })
  if (!chosen) return
  const [first, ...rest] = chosen.paths
  if (!first) return
  void show(first)
  for (const p of rest) ctx.launch('image-viewer', { path: p })
}

// Fullscreen is the browser's, for this window's content.
async function toggleFullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen()
    else await root.value?.requestFullscreen()
  } catch {
    ctx.notify('This browser did not allow fullscreen.', 'error')
  }
}
function onFullscreenChange() {
  fullscreen.value = document.fullscreenElement === root.value
  if (!fullscreen.value) stopSlideshow()
  void nextTick(() => root.value?.focus())
}

let slideTimer: ReturnType<typeof setInterval> | undefined
async function startSlideshow() {
  if (images.value.length < 2) return
  if (!fullscreen.value) await toggleFullscreen()
  slideshow.value = true
  clearInterval(slideTimer)
  slideTimer = setInterval(() => go(1), SLIDE_MS)
}
function stopSlideshow() {
  slideshow.value = false
  clearInterval(slideTimer)
  slideTimer = undefined
}

function onKey(e: KeyboardEvent) {
  const ctrl = e.ctrlKey || e.metaKey
  const key = e.key
  let handled = true
  if (slideshow.value && !ctrl) {
    // Any key ends the slideshow, as in xviewer; the arrows still step.
    stopSlideshow()
    if (key === 'Escape' || key === 'F5') return
  }
  if (ctrl && key.toLowerCase() === 'o') void openImages()
  else if (ctrl && key.toLowerCase() === 'r') rotate(e.shiftKey ? -1 : 1)
  else if (ctrl && (key === '0' || key === ')')) void setZoom(1)
  else if (ctrl) handled = false
  else if (key === 'ArrowRight' || key === 'PageDown' || key === ' ') go(1)
  else if (key === 'ArrowLeft' || key === 'PageUp' || key === 'Backspace') go(-1)
  else if (key === 'Home') goTo(0)
  else if (key === 'End') goTo(images.value.length - 1)
  else if (key === '+' || key === '=') zoomStep(1)
  else if (key === '-') zoomStep(-1)
  else if (key === '1') void setZoom(1)
  else if (key === 'f' || key === 'F') bestFit()
  else if (key === 'Delete') void trash()
  else if (key === 'F11') void toggleFullscreen()
  else if (key === 'F5') void startSlideshow()
  else handled = false
  if (handled) e.preventDefault()
}

let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(([e]) => {
    if (e) stageSize.value = { w: e.contentRect.width, h: e.contentRect.height }
  })
  if (stage.value) ro.observe(stage.value)
  document.addEventListener('fullscreenchange', onFullscreenChange)
  root.value?.focus()
  if (path.value) {
    void show(path.value)
    void loadFolder()
  }
})

onBeforeUnmount(() => {
  ro?.disconnect()
  listAbort?.abort()
  offFs()
  stopSlideshow()
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  if (document.fullscreenElement === root.value) void document.exitFullscreen().catch(() => {})
})

const zoomLabel = computed(() => `${Math.round(zoom.value * 100)}%`)
</script>

<template>
  <div ref="root" class="viewer" :class="{ fullscreen }" tabindex="-1" @keydown="onKey">
    <header class="toolbar">
      <button class="btn" title="Open an image (Ctrl+O)" @click="openImages">Open…</button>
      <span class="sep" />
      <button
        class="btn icon-only"
        title="Previous image (Left)"
        aria-label="Previous image"
        :disabled="images.length < 2"
        @click="go(-1)"
      >
        <SymbolicIcon name="back" />
      </button>
      <button
        class="btn icon-only"
        title="Next image (Right)"
        aria-label="Next image"
        :disabled="images.length < 2"
        @click="go(1)"
      >
        <SymbolicIcon name="forward" />
      </button>
      <span class="sep" />
      <button
        class="btn icon-only"
        title="Zoom out (-)"
        aria-label="Zoom out"
        :disabled="!loaded"
        @click="zoomStep(-1)"
      >
        <SymbolicIcon name="zoom-out" />
      </button>
      <button class="btn icon-only" title="Zoom in (+)" aria-label="Zoom in" :disabled="!loaded" @click="zoomStep(1)">
        <SymbolicIcon name="zoom-in" />
      </button>
      <button
        class="btn"
        :class="{ on: !fit && zoom === 1 }"
        title="Normal size (1)"
        :disabled="!loaded"
        @click="setZoom(1)"
      >
        100%
      </button>
      <button
        class="btn"
        :class="{ on: fit }"
        :aria-pressed="fit"
        title="Best fit (F)"
        :disabled="!loaded"
        @click="bestFit"
      >
        Best Fit
      </button>
      <span class="sep" />
      <button
        class="btn icon-only"
        title="Rotate counterclockwise (Ctrl+Shift+R)"
        aria-label="Rotate counterclockwise"
        :disabled="!loaded"
        @click="rotate(-1)"
      >
        <SymbolicIcon name="rotate-left" />
      </button>
      <button
        class="btn icon-only"
        title="Rotate clockwise (Ctrl+R)"
        aria-label="Rotate clockwise"
        :disabled="!loaded"
        @click="rotate(1)"
      >
        <SymbolicIcon name="rotate-right" />
      </button>
      <span class="spacer" />
      <button
        class="btn icon-only"
        title="Move to Trash (Delete)"
        aria-label="Move to Trash"
        :disabled="!entry"
        @click="trash"
      >
        <SymbolicIcon name="trash" />
      </button>
      <button
        class="btn icon-only"
        title="Slideshow (F5)"
        aria-label="Slideshow"
        :disabled="images.length < 2"
        @click="startSlideshow"
      >
        <SymbolicIcon name="play" />
      </button>
      <button class="btn icon-only" title="Fullscreen (F11)" aria-label="Fullscreen" @click="toggleFullscreen">
        <SymbolicIcon name="fullscreen" />
      </button>
    </header>

    <div
      ref="stage"
      class="stage"
      :class="{ pannable, dragging }"
      @wheel="onWheel"
      @pointerdown="onPointerDown"
      @dblclick="toggleFullscreen"
    >
      <div v-if="entry" class="canvas" :style="{ width: `${box.w}px`, height: `${box.h}px` }">
        <img
          :key="url"
          :src="url"
          :alt="name"
          draggable="false"
          :class="{ ready: loaded, pixelated: zoom >= 3 }"
          :style="{
            width: `${shown.w}px`,
            height: `${shown.h}px`,
            transform: `translate(-50%, -50%) rotate(${turns * 90}deg)`,
          }"
          @load="onLoad"
          @error="onError"
        />
      </div>
      <div v-if="error" class="message" role="status">{{ error }}</div>
      <div v-else-if="!path" class="message empty">
        <ThemeIcon name="xviewer" :size="64" />
        <button class="btn suggested" @click="openImages">Open an Image…</button>
      </div>
    </div>

    <footer v-if="entry" class="status">
      <span v-if="loaded">{{ natural.w }} × {{ natural.h }} pixels</span>
      <span>{{ formatSize(entry.size) }}</span>
      <span v-if="loaded">{{ zoomLabel }}</span>
      <span v-if="index >= 0">{{ index + 1 }} / {{ images.length }}</span>
    </footer>
  </div>
</template>

<style scoped>
.viewer {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
  outline: none;
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
.spacer {
  flex: 1;
}
.stage {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: auto;
  background: var(--image-viewer-bg);
}
.stage.pannable {
  cursor: grab;
}
.stage.dragging {
  cursor: grabbing;
}
.canvas {
  position: relative;
  min-width: 100%;
  min-height: 100%;
}
.canvas img {
  position: absolute;
  left: 50%;
  top: 50%;
  max-width: none;
  visibility: hidden;
  user-select: none;
}
.canvas img.ready {
  visibility: visible;
}
.canvas img.pixelated {
  image-rendering: pixelated;
}
.message {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  padding: 24px;
  text-align: center;
}
.message.empty {
  justify-items: center;
  gap: 16px;
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
.viewer.fullscreen .toolbar,
.viewer.fullscreen .status {
  display: none;
}
.viewer.fullscreen .stage {
  background: var(--image-viewer-fullscreen-bg);
}
</style>
