import { defineStore } from 'pinia'
import { computed, reactive, ref, watch } from 'vue'
import { getApp } from '@/apps/registry'
import { isTile, pushTile as pushTileState, tileRect, type Direction, type Tile } from '@/wm/snap'

export type WindowState = 'normal' | 'minimized' | 'maximized'

/** Things the shell asks of one window's frame (keyboard shortcuts, D53). */
export type FrameAction = 'menu' | 'move' | 'resize'

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
  /**
   * The part of the screen a tiled window fills (D52). rect stays the size
   * it had before, which dragging it out of the tile brings back.
   */
  tile?: Tile
  /** Always on Top: stacked above every window without it. */
  above?: boolean
  z: number
  /** Launch args plus state saved by the app; persisted with the layout. */
  args: Record<string, unknown>
  /**
   * The window this one is a modal dialog of (the file chooser). Dialogs
   * stay above their parent, hide and close with it, are left out of the
   * window list and are not saved with the layout: whoever asked is gone
   * after a reload.
   */
  parentId?: string
}

const LAYOUT_VERSION = 1
/** How much of a window must stay on screen when dragged off an edge. */
const KEEP_VISIBLE = 64
/** Added to the z of Always on Top windows; z counts focus changes, far below this. */
const ABOVE_LAYER = 1_000_000_000

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
  /** The pointer's cursor over that overlay (the window menu's Move and Resize). */
  const cursor = ref('')
  /** Where a window being dragged to an edge would go, shown under it. */
  const snapPreview = ref<{ rect: Rect; z: number } | null>(null)
  /** The latest request to a frame; each frame watches for its own id. */
  const frameRequest = ref<{ id: string; action: FrameAction } | null>(null)
  /** Windows Show Desktop minimized, to bring back on the next press. */
  let desktopShown: string[] | null = null
  /**
   * Windows just opened or closed here, for their animation (D57): not the
   * ones a reload or an unlock brings back, nor the ones logout clears.
   */
  const effects = new Map<string, 'open' | 'close'>()

  let zTop = 10
  let seq = 0
  let storageKey = ''
  const closeGuards = new Map<string, () => boolean | Promise<boolean>>()
  const closeHandlers = new Map<string, Array<() => void>>()

  /** Taskbar order: open order, without dialogs. */
  const ordered = computed(() => windows.value.filter((w) => !w.parentId))

  function get(id: string) {
    return windows.value.find((w) => w.id === id)
  }

  /** The open modal dialog of a window, if any. */
  function dialogOf(id: string) {
    return windows.value.find((w) => w.parentId === id)
  }

  /** Hidden: minimized itself, or a dialog of a hidden window. */
  function isHidden(w: AppWindow): boolean {
    if (w.state === 'minimized') return true
    const parent = w.parentId ? get(w.parentId) : undefined
    return !!parent && isHidden(parent)
  }

  /** Always on Top, by itself or as the dialog of such a window. */
  function isAbove(w: AppWindow): boolean {
    if (w.above) return true
    const parent = w.parentId ? get(w.parentId) : undefined
    return !!parent && isAbove(parent)
  }

  /** The window's z-index: its focus order, within its layer. */
  function stackZ(w: AppWindow): number {
    return w.z + (isAbove(w) ? ABOVE_LAYER : 0)
  }

  /** Where the window is drawn: the whole work area, its tile, or its rect. */
  function shownRect(w: AppWindow): Rect {
    if (w.state === 'maximized') return { x: 0, y: 0, w: area.w, h: area.h }
    if (w.tile) return tileRect(w.tile, area)
    return w.rect
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
    desktopShown = null
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
    effects.set(id, 'open')
    focusedId.value = id
    return id
  }

  /**
   * Opens appId as a modal dialog of parentId, centered over it. The parent
   * stays put but takes no input until the dialog closes.
   */
  function openDialog(parentId: string, appId: string, args: Record<string, unknown> = {}): string | undefined {
    const parent = get(parentId)
    const app = getApp(appId)
    if (!parent || !app || dialogOf(parentId)) return undefined
    const id = `w${Date.now().toString(36)}${(seq++).toString(36)}`
    const size = clampSize(app.defaultSize)
    const p = shownRect(parent)
    const rect = clampRect({ x: p.x + (p.w - size.w) / 2, y: p.y + Math.max(0, (p.h - size.h) / 3), ...size })
    windows.value.push({
      id,
      appId,
      title: app.name,
      icon: app.icon,
      rect,
      state: 'normal',
      z: 0,
      args: { ...args },
      parentId,
    })
    effects.set(id, 'open')
    focus(parentId)
    return id
  }

  /**
   * Raises a window and gives it the focus; a window with a dialog open
   * passes the focus on to the dialog, which comes up just above it.
   */
  function focus(id: string) {
    const w = get(id)
    if (!w) return
    const parent = w.parentId ? get(w.parentId) : undefined
    if (parent && isHidden(parent)) {
      focus(parent.id)
      return
    }
    if (w.state === 'minimized') {
      w.state = w.wasMaximized ? 'maximized' : 'normal'
      desktopShown = null
    }
    if (focusedId.value !== id || w.z !== zTop) w.z = ++zTop
    focusedId.value = id
    const dialog = dialogOf(id)
    if (dialog) focus(dialog.id)
  }

  function focusTopmost(exceptId?: string) {
    const visible = windows.value.filter(
      (w) => !isHidden(w) && !(exceptId && (w.id === exceptId || w.parentId === exceptId)),
    )
    visible.sort((a, b) => stackZ(b) - stackZ(a))
    focusedId.value = visible[0]?.id ?? null
  }

  async function requestClose(id: string) {
    // A window waits for its dialog's answer, as GTK's modal dialogs make it.
    if (dialogOf(id)) {
      focus(id)
      return
    }
    const guard = closeGuards.get(id)
    if (guard && !(await guard())) return
    close(id)
  }

  function close(id: string) {
    const dialog = dialogOf(id)
    if (dialog) close(dialog.id)
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
    const parentId = windows.value[i]!.parentId
    effects.set(id, 'close')
    windows.value.splice(i, 1)
    if (focusedId.value === id) {
      if (parentId && get(parentId) && !isHidden(get(parentId)!)) focusedId.value = parentId
      else focusTopmost()
    }
  }

  function minimize(id: string) {
    const w = get(id)
    if (!w) return
    if (w.parentId) {
      minimize(w.parentId)
      return
    }
    w.wasMaximized = w.state === 'maximized'
    w.state = 'minimized'
    if (focusedId.value === id || get(focusedId.value ?? '')?.parentId === id) focusTopmost(id)
  }

  function toggleMaximize(id: string) {
    const w = get(id)
    if (!w) return
    w.state = w.state === 'maximized' ? 'normal' : 'maximized'
    focus(id)
  }

  /**
   * Ends a drag to an edge (D52): the window fills a tile, keeping its rect
   * for when it comes out again.
   */
  function snapTo(id: string, tile: Tile) {
    const w = get(id)
    if (!w) return
    w.tile = tile
    w.state = 'normal'
    focus(id)
  }

  /** Back to a plain window at rect, out of its tile or maximized state. */
  function restore(id: string) {
    const w = get(id)
    if (!w) return
    w.tile = undefined
    if (w.state === 'maximized') w.state = 'normal'
  }

  /** Leaves a tile where it is: the tile's geometry becomes the window's rect (resizing it). */
  function untile(id: string) {
    const w = get(id)
    if (!w?.tile) return
    w.rect = clampRect(tileRect(w.tile, area))
    w.tile = undefined
  }

  function setAbove(id: string, above: boolean) {
    const w = get(id)
    if (!w) return
    if (above) w.above = true
    else delete w.above
    focus(id)
  }

  /** Super+arrows: one step through the halves and quarters (D53). */
  function pushTile(id: string, dir: Direction) {
    const w = get(id)
    if (!w || w.state === 'minimized') return
    const next = pushTileState(w.tile, w.state === 'maximized', dir)
    w.tile = next.tile
    w.state = next.maximized ? 'maximized' : 'normal'
    focus(id)
  }

  /**
   * Super+D, as in Cinnamon: minimizes every window, and the next press
   * brings back those that are still minimized, in their stacking order.
   * Opening or bringing up a window in between starts afresh.
   */
  function toggleShowDesktop() {
    if (desktopShown) {
      const ids = desktopShown
      desktopShown = null
      for (const id of ids) if (get(id)?.state === 'minimized') focus(id)
      return
    }
    const shown = ordered.value.filter((w) => w.state !== 'minimized').sort((a, b) => stackZ(a) - stackZ(b))
    if (shown.length === 0) return
    for (const w of shown) minimize(w.id)
    desktopShown = shown.map((w) => w.id)
  }

  /** The effect to play for a window entering or leaving, once. */
  function takeEffect(id: string): 'open' | 'close' | undefined {
    const e = effects.get(id)
    effects.delete(id)
    return e
  }

  function requestFrame(id: string, action: FrameAction) {
    if (get(id)) frameRequest.value = { id, action }
  }

  /** Taskbar click: minimize the focused window, otherwise bring it up. */
  function activate(id: string) {
    const w = get(id)
    if (!w) return
    const active = focusedId.value === id || (!!focusedId.value && get(focusedId.value)?.parentId === id)
    if (active && w.state !== 'minimized') minimize(id)
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
    desktopShown = null
    try {
      const raw = localStorage.getItem(storageKey)
      if (!raw) return
      const saved = JSON.parse(raw) as { version: number; windows: AppWindow[]; focusedId?: string }
      if (saved.version !== LAYOUT_VERSION || !Array.isArray(saved.windows)) return
      windows.value = saved.windows.filter((w) => getApp(w.appId) && !w.parentId)
      for (const w of windows.value) if (w.tile !== undefined && !isTile(w.tile)) delete w.tile
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
    desktopShown = null
    effects.clear()
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
        JSON.stringify({
          version: LAYOUT_VERSION,
          windows: windows.value.filter((w) => !w.parentId),
          focusedId: get(focusedId.value ?? '')?.parentId ?? focusedId.value,
        }),
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
  // A reload within the delay still finds the last change.
  window.addEventListener('pagehide', () => {
    if (timer !== undefined) {
      clearTimeout(timer)
      save()
    }
  })

  return {
    windows,
    ordered,
    focusedId,
    area,
    interacting,
    cursor,
    snapPreview,
    frameRequest,
    get,
    dialogOf,
    isHidden,
    stackZ,
    shownRect,
    open,
    openDialog,
    focus,
    requestClose,
    close,
    minimize,
    toggleMaximize,
    snapTo,
    restore,
    untile,
    setAbove,
    pushTile,
    toggleShowDesktop,
    requestFrame,
    takeEffect,
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
