// Window animations (D57), as Cinnamon's "traditional" effects: minimizing
// shrinks a window into its window-list button and unminimizing grows it
// back out; maximizing, restoring and tiling move it from where it was.
// The frame takes its new geometry at once, so its content (a terminal)
// lays out and resizes once, and a transform plays the old box into the
// new one, scaling the content on the way, as Muffin scales a window.
import { nextTick, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import type { AppWindow } from '@/stores/windows'
import { useSettings } from '@/stores/settings'
import { useWindows } from '@/stores/windows'

export const MINIMIZE_MS = 200
export const GEOMETRY_MS = 160
export const OPEN_MS = 150
export const CLOSE_MS = 120
/** How small an opening window starts and a closing one ends. */
const OPEN_SCALE = 'scale(0.92)'

export interface Box {
  left: number
  top: number
  width: number
  height: number
}

/**
 * The transform that draws an element laid out at `at` in the box `shown`,
 * with its origin at the top left.
 */
export function boxTransform(shown: Box, at: Box): string {
  const sx = shown.width / Math.max(1, at.width)
  const sy = shown.height / Math.max(1, at.height)
  const dx = shown.left - at.left
  const dy = shown.top - at.top
  return `translate(${round(dx)}px, ${round(dy)}px) scale(${round(sx, 4)}, ${round(sy, 4)})`
}

function round(n: number, places = 2) {
  const f = 10 ** places
  return Math.round(n * f) / f
}

export function sameBox(a: Box, b: Box): boolean {
  return (
    Math.abs(a.left - b.left) < 1 &&
    Math.abs(a.top - b.top) < 1 &&
    Math.abs(a.width - b.width) < 1 &&
    Math.abs(a.height - b.height) < 1
  )
}

/** Where a window minimizes to: its window-list button, or its parent's (dialogs). */
function buttonBox(win: AppWindow): Box | null {
  for (const id of [win.id, win.parentId]) {
    if (!id) continue
    const el = document.querySelector(`[data-window="${CSS.escape(id)}"]`)
    if (el) return el.getBoundingClientRect()
  }
  return null
}

/** Reduced motion asked for by the system, or animations off in Tilde's settings (D72). */
function reducedMotion(): boolean {
  if (!useSettings().settings.effects.animations) return true
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
}

function canAnimate(el: Element): el is HTMLElement {
  return el instanceof HTMLElement && typeof el.animate === 'function' && !reducedMotion()
}

/**
 * A window opening grows a little from its centre as it fades in; one
 * closing shrinks and fades out, after which `done` takes it away. Hidden
 * (minimized) windows go at once.
 */
export function animateWindow(el: Element, effect: 'open' | 'close' | undefined, done: () => void) {
  if (!effect || !canAnimate(el) || el.classList.contains('minimized')) {
    done()
    return
  }
  const shown = { transform: 'none', opacity: 1 }
  const small = { transform: OPEN_SCALE, opacity: 0 }
  if (effect === 'close') {
    el.style.pointerEvents = 'none'
    el.inert = true
  }
  const a = el.animate(effect === 'open' ? [small, shown] : [shown, small], {
    duration: effect === 'open' ? OPEN_MS : CLOSE_MS,
    easing: effect === 'open' ? 'ease-out' : 'ease-in',
  })
  a.onfinish = () => done()
  a.oncancel = () => done()
}

/**
 * Animates a window frame's state changes. Returns `leaving`: true while a
 * minimizing window plays out, so the frame stays drawn until it is done.
 */
export function useFrameAnimation(el: Ref<HTMLElement | null>, win: () => AppWindow, hidden: () => boolean) {
  const wm = useWindows()
  const leaving = ref(false)
  let running: Animation | null = null

  const stop = () => {
    running?.cancel()
    running = null
    leaving.value = false
  }

  const play = (node: HTMLElement, frames: Keyframe[], ms: number, done?: () => void) => {
    const a = node.animate(frames, { duration: ms, easing: 'cubic-bezier(0.25, 0.1, 0.25, 1)' })
    running = a
    a.onfinish = () => {
      if (running === a) running = null
      done?.()
    }
  }

  watch(
    () => ({ hidden: hidden(), state: win().state, tile: win().tile }),
    (now, before) => {
      const node = el.value
      // Drags change the state on their own (out of maximized, or a tile);
      // the drag shows the movement.
      if (!node || typeof node.animate !== 'function' || reducedMotion() || wm.interacting) return
      // Where it is drawn now, before this change renders (during a drop
      // into a tile, where the pointer let go), once an opening has played.
      stop()
      for (const a of node.getAnimations?.() ?? []) a.finish()
      const from = node.getBoundingClientRect()

      if (now.hidden && !before.hidden) {
        const target = buttonBox(win())
        if (!target) return
        leaving.value = true
        void nextTick(() => {
          const at = node.getBoundingClientRect()
          play(
            node,
            [
              { transform: boxTransform(from, at), opacity: 1, transformOrigin: '0 0' },
              { transform: boxTransform(target, at), opacity: 0, transformOrigin: '0 0' },
            ],
            MINIMIZE_MS,
            () => {
              leaving.value = false
            },
          )
        })
        return
      }

      void nextTick(() => {
        const at = node.getBoundingClientRect()
        if (now.hidden) return
        if (before.hidden) {
          const source = buttonBox(win())
          if (!source) return
          play(
            node,
            [
              { transform: boxTransform(source, at), opacity: 0, transformOrigin: '0 0' },
              { transform: 'none', opacity: 1, transformOrigin: '0 0' },
            ],
            MINIMIZE_MS,
          )
          return
        }
        if (sameBox(from, at)) return
        play(
          node,
          [
            { transform: boxTransform(from, at), transformOrigin: '0 0' },
            { transform: 'none', transformOrigin: '0 0' },
          ],
          GEOMETRY_MS,
        )
      })
    },
    { flush: 'pre' },
  )

  onBeforeUnmount(stop)
  return { leaving }
}
