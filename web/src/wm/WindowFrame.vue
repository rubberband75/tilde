<script setup lang="ts">
// One window: Mint-Y title bar, drag, eight resize handles, minimize,
// maximize and close. Geometry lives in the window store; during a drag the
// frame moves with a CSS transform and commits the position on release,
// which avoids relayout on every pointer move.
import { computed, ref } from 'vue'
import { appComponent, getApp } from '@/apps/registry'
import ThemeIcon from '@/components/ThemeIcon.vue'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { createContext } from '@/shell/actions'
import { useWindows, type AppWindow, type Rect } from '@/stores/windows'
import { currentStyle, type WindowButton } from '@/theme/desktopStyle'

const props = defineProps<{ win: AppWindow }>()
const wm = useWindows()

const app = computed(() => getApp(props.win.appId))
const comp = computed(() => appComponent(props.win.appId))
const ctx = createContext(props.win.id)

const focused = computed(() => wm.focusedId === props.win.id)
const maximized = computed(() => props.win.state === 'maximized')
const minimized = computed(() => props.win.state === 'minimized')

const content = ref<HTMLElement | null>(null)
let lastFocus: HTMLElement | null = null

function onFocusIn(e: FocusEvent) {
  if (e.target instanceof HTMLElement) lastFocus = e.target
}

/**
 * Pressing on the frame (title bar, buttons, resize edges) keeps keyboard
 * focus in the window, as in Cinnamon: the widget inside that last had focus
 * gets it back. Without preventDefault the browser would move focus to the
 * body, because the title bar is not focusable, undoing the focus an app
 * (the terminal) takes when its window is raised.
 */
function keepFocus(e: MouseEvent) {
  e.preventDefault()
  if (content.value?.contains(document.activeElement)) return
  if (lastFocus?.isConnected && content.value?.contains(lastFocus)) lastFocus.focus({ preventScroll: true })
}

const drag = ref<{ dx: number; dy: number } | null>(null)
const live = ref<Rect | null>(null)

const style = computed(() => {
  const z = props.win.z
  if (maximized.value) return { left: '0px', top: '0px', width: `${wm.area.w}px`, height: `${wm.area.h}px`, zIndex: z }
  const r = live.value ?? props.win.rect
  return {
    left: `${r.x}px`,
    top: `${r.y}px`,
    width: `${r.w}px`,
    height: `${r.h}px`,
    zIndex: z,
    transform: drag.value ? `translate3d(${drag.value.dx}px, ${drag.value.dy}px, 0)` : undefined,
  }
})

function startDrag(e: PointerEvent) {
  if (e.button !== 0) return
  wm.focus(props.win.id)
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  const sx = e.clientX
  const sy = e.clientY
  let base: Rect = { ...props.win.rect }
  let moved = false

  const onMove = (ev: PointerEvent) => {
    const dx = ev.clientX - sx
    const dy = ev.clientY - sy
    if (!moved) {
      if (Math.abs(dx) + Math.abs(dy) < 4) return
      moved = true
      wm.interacting = true
      if (maximized.value) {
        // Leave maximized under the pointer, keeping the grab point at the
        // same relative position along the title bar, as Cinnamon does.
        const ratio = sx / Math.max(1, wm.area.w)
        base = { ...props.win.rect, x: sx - props.win.rect.w * ratio, y: 0 }
        wm.setRect(props.win.id, base)
        wm.toggleMaximize(props.win.id)
      }
    }
    drag.value = { dx, dy }
  }
  const onUp = (ev: PointerEvent) => {
    el.releasePointerCapture(ev.pointerId)
    el.removeEventListener('pointermove', onMove)
    el.removeEventListener('pointerup', onUp)
    el.removeEventListener('pointercancel', onUp)
    if (moved && drag.value) {
      wm.setRect(props.win.id, { ...base, x: base.x + drag.value.dx, y: base.y + drag.value.dy })
    }
    drag.value = null
    wm.interacting = false
  }
  el.addEventListener('pointermove', onMove)
  el.addEventListener('pointerup', onUp)
  el.addEventListener('pointercancel', onUp)
}

const edges = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'] as const
type Edge = (typeof edges)[number]

function startResize(e: PointerEvent, edge: Edge) {
  if (e.button !== 0 || maximized.value) return
  wm.focus(props.win.id)
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  const start = { ...props.win.rect }
  const min = app.value?.minSize ?? { w: 240, h: 140 }
  const sx = e.clientX
  const sy = e.clientY
  wm.interacting = true

  const onMove = (ev: PointerEvent) => {
    const dx = ev.clientX - sx
    const dy = ev.clientY - sy
    let { x, y, w, h } = start
    if (edge.includes('e')) w = Math.max(min.w, start.w + dx)
    if (edge.includes('s')) h = Math.max(min.h, start.h + dy)
    if (edge.includes('w')) {
      w = Math.max(min.w, start.w - dx)
      x = start.x + start.w - w
    }
    if (edge.includes('n')) {
      h = Math.max(min.h, start.h - dy)
      y = start.y + start.h - h
      if (y < 0) {
        h += y
        y = 0
      }
    }
    live.value = { x, y, w, h }
  }
  const onUp = (ev: PointerEvent) => {
    el.releasePointerCapture(ev.pointerId)
    el.removeEventListener('pointermove', onMove)
    el.removeEventListener('pointerup', onUp)
    el.removeEventListener('pointercancel', onUp)
    if (live.value) wm.setRect(props.win.id, live.value)
    live.value = null
    wm.interacting = false
  }
  el.addEventListener('pointermove', onMove)
  el.addEventListener('pointerup', onUp)
  el.addEventListener('pointercancel', onUp)
}

function press(button: WindowButton) {
  if (button === 'minimize') wm.minimize(props.win.id)
  else if (button === 'maximize') wm.toggleMaximize(props.win.id)
  else void wm.requestClose(props.win.id)
}

function buttonIcon(button: WindowButton) {
  if (button === 'maximize') return maximized.value ? 'restore' : 'maximize'
  return button
}

const buttonLabels: Record<WindowButton, string> = { minimize: 'Minimize', maximize: 'Maximize', close: 'Close' }
</script>

<template>
  <section
    class="window"
    :class="{ focused, maximized, minimized, dragging: !!drag }"
    :style="style"
    role="dialog"
    :aria-label="win.title"
    @pointerdown.capture="wm.focus(win.id)"
  >
    <header class="titlebar" @pointerdown="startDrag" @mousedown="keepFocus" @dblclick="wm.toggleMaximize(win.id)">
      <div class="buttons left">
        <button
          v-for="b in currentStyle.buttons.left"
          :key="b"
          class="wm-button"
          :class="b"
          :title="buttonLabels[b]"
          @pointerdown.stop
          @dblclick.stop
          @click="press(b)"
        >
          <SymbolicIcon :name="buttonIcon(b)" :size="14" />
        </button>
      </div>
      <ThemeIcon class="title-icon" :name="win.icon" :size="16" />
      <span class="title">{{ win.title }}</span>
      <div class="buttons right">
        <button
          v-for="b in currentStyle.buttons.right"
          :key="b"
          class="wm-button"
          :class="b"
          :title="buttonLabels[b]"
          @pointerdown.stop
          @dblclick.stop
          @click="press(b)"
        >
          <SymbolicIcon :name="buttonIcon(b)" :size="14" />
        </button>
      </div>
    </header>
    <div ref="content" class="content" @focusin="onFocusIn">
      <component :is="comp" v-if="comp" :ctx="ctx" />
    </div>
    <template v-if="!maximized">
      <div
        v-for="edge in edges"
        :key="edge"
        class="handle"
        :class="edge"
        @pointerdown.stop="startResize($event, edge)"
        @mousedown="keepFocus"
      />
    </template>
  </section>
</template>

<style scoped>
.window {
  position: absolute;
  display: flex;
  flex-direction: column;
  background: var(--window-bg);
  color: var(--window-fg);
  border: 1px solid var(--wm-border);
  border-radius: var(--wm-radius) var(--wm-radius) 0 0;
  box-shadow: var(--wm-shadow-unfocused);
  contain: layout;
}
.window.focused {
  box-shadow: var(--wm-shadow);
}
.window.maximized {
  border-radius: 0;
  border: none;
}
.window.minimized {
  display: none;
}
.window.dragging {
  will-change: transform;
}

.titlebar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  height: var(--wm-titlebar-height);
  padding: 0 4px 0 10px;
  background: var(--wm-titlebar-bg-unfocused);
  color: var(--wm-title-fg-unfocused);
  border-radius: inherit;
  cursor: default;
  touch-action: none;
}
.focused .titlebar {
  background: var(--wm-titlebar-bg);
  color: var(--wm-title-fg);
}
.title-icon {
  flex: none;
}
.title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-weight: bold;
  text-align: center;
}
.buttons {
  display: flex;
  gap: 2px;
}
.buttons.left:empty {
  display: none;
}
.wm-button {
  display: grid;
  place-items: center;
  width: var(--wm-button-size);
  height: var(--wm-button-size);
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--wm-button-fg);
}
.wm-button:hover {
  background: var(--wm-button-hover-bg);
}
.wm-button.close:hover {
  background: var(--wm-close-hover-bg);
  color: var(--wm-close-hover-fg);
}

.content {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.handle {
  position: absolute;
  z-index: 2;
  touch-action: none;
}
.handle.n,
.handle.s {
  left: 8px;
  right: 8px;
  height: var(--wm-resize-handle);
  cursor: ns-resize;
}
.handle.n {
  top: calc(var(--wm-resize-handle) / -2);
}
.handle.s {
  bottom: calc(var(--wm-resize-handle) / -2);
}
.handle.e,
.handle.w {
  top: 8px;
  bottom: 8px;
  width: var(--wm-resize-handle);
  cursor: ew-resize;
}
.handle.e {
  right: calc(var(--wm-resize-handle) / -2);
}
.handle.w {
  left: calc(var(--wm-resize-handle) / -2);
}
.handle.ne,
.handle.nw,
.handle.se,
.handle.sw {
  width: 12px;
  height: 12px;
}
.handle.ne {
  top: -6px;
  right: -6px;
  cursor: nesw-resize;
}
.handle.sw {
  bottom: -6px;
  left: -6px;
  cursor: nesw-resize;
}
.handle.nw {
  top: -6px;
  left: -6px;
  cursor: nwse-resize;
}
.handle.se {
  bottom: -6px;
  right: -6px;
  cursor: nwse-resize;
}
</style>
