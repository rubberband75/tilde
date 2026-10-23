import { defineStore } from 'pinia'
import { computed, reactive, ref, watch } from 'vue'
import { getApp } from '@/apps/registry'

export type WindowState = 'normal' | 'minimized' | 'maximized'

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export interface AppWindow {
  /** Stable id; apps map it to worker resources (D13). */
  id: string
  appId: string
  title: string
  icon: string
  rect: Rect
  state: WindowState
  /** Whether a minimized window was maximized before. */
  wasMaximized?: boolean
  z: number
  /** Launch args plus state saved by the app; persisted with the layout. */
  args: Record<string, unknown>
}

const LAYOUT_VERSION = 1
/** How much of a window must stay on screen when dragged off an edge. */
const KEEP_VISIBLE = 64

/**
 * The window manager's state (docs/architecture.md, "Window manager").
 * Layout is kept per user in localStorage, so a refresh restores the
 * desktop; running things (terminals) live in the worker.
 */
export const useWindows = defineStore('windows', () => {
  const windows = ref<AppWindow[]>([])
  const focusedId = ref<string | null>(null)
  /** Area windows may occupy: the screen minus the panel. */
  const area = reactive({ w: window.innerWidth, h: window.innerHeight - 40 })
  /** True during a drag or resize; shows an overlay over iframes/terminals. */
  const interacting = ref(false)

  let zTop = 10
  let seq = 0
  let storageKey = ''
  const closeGuards = new Map<string, () => boolean | Promise<boolean>>()
  const closeHandlers = new Map<string, Array<() => void>>()

  const ordered = computed(() => windows.value) // taskbar order: open order

  function get(id: string) {
    return windows.value.find((w) => w.id === id)
  }

  function clampSize(size: { w: number; h: number }) {
    return { w: Math.min(size.w, Math.max(200, area.w - 20)), h: Math.min(size.h, Math.max(150, area.h - 20)) }
  }

  /** Keeps the title bar reachable inside the work area. */
  function clampRect(r: Rect): Rect {
    const x = Math.min(Math.max(r.x, KEEP_VISIBLE - r.w), area.w - KEEP_VISIBLE)
    const y = Math.min(Math.max(r.y, 0), area.h - 32)
    return { x: Math.round(x), y: Math.round(y), w: Math.round(r.w), h: Math.round(r.h) }
  }

  function open(appId: string, args: Record<string, unknown> = {}): string | undefined {
    const app = getApp(appId)
    if (!app) return undefined
    if (app.singleInstance) {
      const existing = windows.value.find((w) => w.appId === appId)
      if (existing) {
        focus(existing.id)
        return existing.id
      }
    }
    const id = `w${Date.now().toString(36)}${(seq++).toString(36)}`
    const size = clampSize(app.defaultSize)
    const cascade = (windows.value.filter((w) => w.state !== 'minimized').length % 8) * 28
    const rect = clampRect({
      x: Math.round((area.w - size.w) / 2) - 100 + cascade,
      y: Math.round((area.h - size.h) / 2) - 60 + cascade,
      ...size,
    })
    windows.value.push({
      id,
      appId,
      title: app.name,
      icon: app.icon,
      rect,
      state: 'normal',
      z: ++zTop,
      args: { ...args },
    })
    focusedId.value = id
    return id
  }

  function focus(id: string) {
    const w = get(id)
    if (!w) return
    if (w.state === 'minimized') w.state = w.wasMaximized ? 'maximized' : 'normal'
    if (focusedId.value !== id || w.z !== zTop) w.z = ++zTop
    focusedId.value = id
  }

  function focusTopmost(exceptId?: string) {
    const visible = windows.value.filter((w) => w.state !== 'minimized' && w.id !== exceptId)
    visible.sort((a, b) => b.z - a.z)
    focusedId.value = visible[0]?.id ?? null
  }

  async function requestClose(id: string) {
    const guard = closeGuards.get(id)
    if (guard && !(await guard())) return
    close(id)
  }

  function close(id: string) {
    const i = windows.value.findIndex((w) => w.id === id)
    if (i < 0) return
    for (const fn of closeHandlers.get(id) ?? []) {
      try {
        fn()
      } catch (e) {
        console.error('close handler failed', e)
      }
    }
    closeHandlers.delete(id)
    closeGuards.delete(id)
    windows.value.splice(i, 1)
    if (focusedId.value === id) focusTopmost()
  }

  function minimize(id: string) {
    const w = get(id)
    if (!w) return
    w.wasMaximized = w.state === 'maximized'
    w.state = 'minimized'
    if (focusedId.value === id) focusTopmost(id)
  }

  function toggleMaximize(id: string) {
    const w = get(id)
    if (!w) return
    w.state = w.state === 'maximized' ? 'normal' : 'maximized'
    focus(id)
  }

  /** Taskbar click: minimize the focused window, otherwise bring it up. */
  function activate(id: string) {
    const w = get(id)
    if (!w) return
    if (focusedId.value === id && w.state !== 'minimized') minimize(id)
    else focus(id)
  }

  function setRect(id: string, rect: Rect) {
    const w = get(id)
    if (w) w.rect = clampRect(rect)
  }

  function setTitle(id: string, title: string) {
    const w = get(id)
    if (w && w.title !== title) w.title = title
  }

  function setIcon(id: string, icon: string) {
    const w = get(id)
    if (w && w.icon !== icon) w.icon = icon
  }

  function saveState(id: string, patch: Record<string, unknown>) {
    const w = get(id)
    if (w) Object.assign(w.args, patch)
  }

  function setCloseGuard(id: string, guard: (() => boolean | Promise<boolean>) | null) {
    if (guard) closeGuards.set(id, guard)
    else closeGuards.delete(id)
  }

  function onClose(id: string, fn: () => void) {
    const list = closeHandlers.get(id) ?? []
    list.push(fn)
    closeHandlers.set(id, list)
  }

  function setArea(w: number, h: number) {
    area.w = w
    area.h = h
  }

  // Persistence, per user.
  function load(user: string) {
    storageKey = `tilde:layout:${user}`
    windows.value = []
    focusedId.value = null
    try {
      const raw = localStorage.getItem(storageKey)
      if (!raw) return
      const saved = JSON.parse(raw) as { version: number; windows: AppWindow[]; focusedId?: string }
      if (saved.version !== LAYOUT_VERSION || !Array.isArray(saved.windows)) return
      windows.value = saved.windows.filter((w) => getApp(w.appId))
      zTop = Math.max(10, ...windows.value.map((w) => w.z))
      focusedId.value = saved.focusedId && get(saved.focusedId) ? saved.focusedId : null
    } catch {
      // Storage can be unavailable (private windows); start empty.
    }
  }

  /**
   * Forget windows without running close handlers (logout, lock, expiry).
   * A change still waiting to be saved is saved first: unlocking comes
   * back to this layout (D40).
   */
  function reset() {
    if (timer !== undefined) {
      clearTimeout(timer)
      save()
    }
    storageKey = ''
    windows.value = []
    focusedId.value = null
    closeGuards.clear()
    closeHandlers.clear()
  }

  let timer: ReturnType<typeof setTimeout> | undefined
  function save() {
    timer = undefined
    if (!storageKey) return
    try {
      localStorage.setItem(
        storageKey,
        JSON.stringify({ version: LAYOUT_VERSION, windows: windows.value, focusedId: focusedId.value }),
      )
    } catch {
      // ignore quota and privacy-mode errors
    }
  }
  watch(
    [windows, focusedId],
    () => {
      if (!storageKey) return
      clearTimeout(timer)
      timer = setTimeout(save, 300)
    },
    { deep: true },
  )

  return {
    windows,
    ordered,
    focusedId,
    area,
    interacting,
    get,
    open,
    focus,
    requestClose,
    close,
    minimize,
    toggleMaximize,
    activate,
    setRect,
    setTitle,
    setIcon,
    saveState,
    setCloseGuard,
    onClose,
    setArea,
    load,
    reset,
  }
})
