<script setup lang="ts">
// One window: Mint-Y title bar, drag, eight resize handles, minimize,
// maximize and close, edge tiling and the window menu (D52). Geometry lives
// in the window store; during a drag the frame moves with a CSS transform
// and commits the position on release, which avoids relayout on every
// pointer move.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
  ContextMenuCheckboxItem,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuItemIndicator,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from 'reka-ui'
import { appComponent, getApp } from '@/apps/registry'
import ThemeIcon from '@/components/ThemeIcon.vue'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { createContext } from '@/shell/actions'
import { useWindows, type AppWindow, type Rect } from '@/stores/windows'
import { currentStyle, type WindowButton } from '@/theme/desktopStyle'
import { snapTarget, tileRect, type Tile } from './snap'

const props = defineProps<{ win: AppWindow }>()
const wm = useWindows()

const app = computed(() => getApp(props.win.appId))
const comp = computed(() => appComponent(props.win.appId))
const ctx = createContext(props.win.id)

const focused = computed(() => wm.focusedId === props.win.id)
const maximized = computed(() => props.win.state === 'maximized')
const tiled = computed(() => !maximized.value && !!props.win.tile)
const minimized = computed(() => wm.isHidden(props.win))
/** A modal dialog of this window is open: the content takes no input. */
const blocked = computed(() => !!wm.dialogOf(props.win.id))

const content = ref<HTMLElement | null>(null)
let lastFocus: HTMLElement | null = null

/** Gives keyboard focus back to the widget inside that had it last. */
function refocus() {
  if (lastFocus?.isConnected && content.value?.contains(lastFocus)) lastFocus.focus({ preventScroll: true })
}

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
  refocus()
}

// When its dialog closes, the window's focus goes back where it was.
watch(blocked, (now, before) => {
  if (before && !now && focused.value) void nextTick(refocus)
})

const drag = ref<{ dx: number; dy: number } | null>(null)
const live = ref<Rect | null>(null)

const style = computed(() => {
  const r = (!maximized.value && live.value) || wm.shownRect(props.win)
  return {
    left: `${r.x}px`,
    top: `${r.y}px`,
    width: `${r.w}px`,
    height: `${r.h}px`,
    zIndex: wm.stackZ(props.win),
    transform: drag.value ? `translate3d(${drag.value.dx}px, ${drag.value.dy}px, 0)` : undefined,
  }
})

function startDrag(e: PointerEvent) {
  if (e.button !== 0) return
  wm.focus(props.win.id)
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  // Pointer positions are taken relative to the work area.
  const layer = el.closest('.window-layer')?.getBoundingClientRect() ?? { left: 0, top: 0 }
  const sx = e.clientX - layer.left
  const sy = e.clientY - layer.top
  let base: Rect = { ...props.win.rect }
  let moved = false
  let target: Tile | null = null

  // The edge tiling preview, under the window (D52).
  const showSnap = (t: Tile | null) => {
    if (t === target) return
    target = t
    wm.snapPreview = t ? { rect: tileRect(t, wm.area), z: wm.stackZ(props.win) } : null
  }

  const onMove = (ev: PointerEvent) => {
    const dx = ev.clientX - layer.left - sx
    const dy = ev.clientY - layer.top - sy
    if (!moved) {
      if (Math.abs(dx) + Math.abs(dy) < 4) return
      moved = true
      wm.interacting = true
      if (maximized.value || tiled.value) {
        // Leave maximized or the tile under the pointer, keeping the grab
        // point at the same relative position along the title bar, as
        // Cinnamon does; the window gets its own size back.
        const shown = wm.shownRect(props.win)
        const ratio = (sx - shown.x) / Math.max(1, shown.w)
        wm.setRect(props.win.id, { ...props.win.rect, x: sx - props.win.rect.w * ratio, y: shown.y })
        wm.restore(props.win.id)
        base = { ...props.win.rect }
      }
    }
    drag.value = { dx, dy }
    showSnap(snapTarget(sx + dx, sy + dy, wm.area))
  }
  const onUp = (ev: PointerEvent) => {
    el.releasePointerCapture(ev.pointerId)
    el.removeEventListener('pointermove', onMove)
    el.removeEventListener('pointerup', onUp)
    el.removeEventListener('pointercancel', onUp)
    if (moved && drag.value) {
      // A tiled window keeps base, the rect it had before, for later.
      if (target && ev.type === 'pointerup') wm.snapTo(props.win.id, target)
      else wm.setRect(props.win.id, { ...base, x: base.x + drag.value.dx, y: base.y + drag.value.dy })
    }
    wm.snapPreview = null
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
  // Resizing a tile makes it a plain window where it is.
  wm.untile(props.win.id)
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

/** Arrow-key step for the window menu's Move and Resize, in pixels. */
const KEY_STEP = 10
let endKeyboardMode: ((keep: boolean) => void) | null = null

/**
 * The window menu's Move and Resize, as in Cinnamon: moving the pointer
 * (no button held) or the arrow keys move the window or its bottom-right
 * corner; a click or Enter keeps the change, Escape or a right click
 * undoes it. Browsers cannot warp the pointer, so it moves the window by
 * how far it goes rather than taking it along.
 */
function startKeyboardMode(mode: 'move' | 'resize') {
  if (maximized.value || endKeyboardMode) return
  wm.focus(props.win.id)
  wm.untile(props.win.id)
  const id = props.win.id
  const start = { ...props.win.rect }
  const min = app.value?.minSize ?? { w: 240, h: 140 }
  let dx = 0
  let dy = 0
  let last: { x: number; y: number } | null = null
  const apply = () => {
    live.value =
      mode === 'move'
        ? { ...start, x: start.x + dx, y: start.y + dy }
        : { ...start, w: Math.max(min.w, start.w + dx), h: Math.max(min.h, start.h + dy) }
  }

  const onMove = (ev: PointerEvent) => {
    if (last) {
      dx += ev.clientX - last.x
      dy += ev.clientY - last.y
      apply()
    }
    last = { x: ev.clientX, y: ev.clientY }
  }
  const steps: Record<string, [number, number]> = {
    ArrowLeft: [-KEY_STEP, 0],
    ArrowRight: [KEY_STEP, 0],
    ArrowUp: [0, -KEY_STEP],
    ArrowDown: [0, KEY_STEP],
  }
  // Every key goes to the mode, not to the app (the lock shortcut, caught
  // earlier on the window, still works).
  const onKey = (ev: KeyboardEvent) => {
    ev.preventDefault()
    ev.stopPropagation()
    const step = steps[ev.key]
    if (step) {
      dx += step[0]
      dy += step[1]
      apply()
    } else if (ev.key === 'Enter' || ev.key === ' ') end(true)
    else if (ev.key === 'Escape') end(false)
  }
  const onDown = (ev: PointerEvent) => {
    ev.preventDefault()
    ev.stopPropagation()
    end(ev.button === 0)
  }

  const end = (keep: boolean) => {
    window.removeEventListener('pointermove', onMove, true)
    window.removeEventListener('keydown', onKey, true)
    window.removeEventListener('pointerdown', onDown, true)
    if (keep && live.value) wm.setRect(id, live.value)
    live.value = null
    wm.interacting = false
    wm.cursor = ''
    endKeyboardMode = null
    refocus()
  }
  endKeyboardMode = end
  apply()
  wm.interacting = true
  wm.cursor = mode === 'move' ? 'move' : 'se-resize'
  window.addEventListener('pointermove', onMove, true)
  window.addEventListener('keydown', onKey, true)
  window.addEventListener('pointerdown', onDown, true)
}

onBeforeUnmount(() => endKeyboardMode?.(false))

const titlebar = ref<HTMLElement | null>(null)

/** Opens the window menu under the title bar's left end, as Alt+Space does in Cinnamon. */
function openMenu() {
  const r = titlebar.value?.getBoundingClientRect()
  if (!r) return
  titlebar.value!.dispatchEvent(
    new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: r.left + 8, clientY: r.bottom }),
  )
}

// Keyboard shortcuts for this window (D53) come through the store.
watch(
  () => wm.frameRequest,
  (req) => {
    if (req?.id !== props.win.id) return
    wm.frameRequest = null
    if (req.action === 'menu') openMenu()
    else startKeyboardMode(req.action)
  },
)

/** The menu gives focus to the title bar when it closes; the window's widget should have it. */
function onMenuClosed(e: Event) {
  e.preventDefault()
  refocus()
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
    :class="{ focused, maximized, tiled, minimized, dragging: !!drag }"
    :style="style"
    role="dialog"
    :aria-label="win.title"
    @pointerdown.capture="wm.focus(win.id)"
  >
    <ContextMenuRoot :modal="false">
      <ContextMenuTrigger as-child>
        <header
          ref="titlebar"
          class="titlebar"
          @pointerdown="startDrag"
          @mousedown="keepFocus"
          @dblclick="wm.toggleMaximize(win.id)"
        >
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
      </ContextMenuTrigger>
      <ContextMenuPortal>
        <ContextMenuContent class="popup-menu" @close-auto-focus="onMenuClosed">
          <ContextMenuItem class="item" @select="wm.minimize(win.id)">Minimize</ContextMenuItem>
          <ContextMenuItem class="item" @select="wm.toggleMaximize(win.id)">
            {{ maximized ? 'Unmaximize' : 'Maximize' }}
          </ContextMenuItem>
          <ContextMenuItem class="item" :disabled="maximized" @select="startKeyboardMode('move')">Move</ContextMenuItem>
          <ContextMenuItem class="item" :disabled="maximized" @select="startKeyboardMode('resize')">
            Resize
          </ContextMenuItem>
          <!-- A dialog is stacked with its parent. -->
          <template v-if="!win.parentId">
            <ContextMenuSeparator class="separator" />
            <ContextMenuCheckboxItem
              class="item"
              :model-value="!!win.above"
              @update:model-value="wm.setAbove(win.id, $event === true)"
            >
              <ContextMenuItemIndicator class="indicator"
                ><SymbolicIcon name="check" :size="14"
              /></ContextMenuItemIndicator>
              Always on Top
            </ContextMenuCheckboxItem>
          </template>
          <ContextMenuSeparator class="separator" />
          <ContextMenuItem class="item" @select="wm.requestClose(win.id)">Close</ContextMenuItem>
        </ContextMenuContent>
      </ContextMenuPortal>
    </ContextMenuRoot>
    <div ref="content" class="content" :inert="blocked" @focusin="onFocusIn">
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
/* GTK squares the corners of tiled windows. */
.window.tiled {
  border-radius: 0;
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
