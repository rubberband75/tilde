import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWindows } from './windows'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('open', () => {
  it('opens a focused window on top, inside the work area', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    expect(wm.windows).toHaveLength(2)
    expect(wm.focusedId).toBe(b)
    expect(wm.get(b)!.z).toBeGreaterThan(wm.get(a)!.z)
    for (const w of wm.windows) {
      expect(w.rect.y).toBeGreaterThanOrEqual(0)
      expect(w.rect.x + w.rect.w).toBeGreaterThan(0)
      expect(w.rect.x).toBeLessThan(wm.area.w)
    }
  })

  it('focuses the existing window of a single-instance app', () => {
    const wm = useWindows()
    const first = wm.open('system-info')
    wm.open('files')
    expect(wm.open('system-info')).toBe(first)
    expect(wm.windows.filter((w) => w.appId === 'system-info')).toHaveLength(1)
    expect(wm.focusedId).toBe(first)
  })

  it('ignores unknown apps', () => {
    const wm = useWindows()
    expect(wm.open('no-such-app')).toBeUndefined()
    expect(wm.windows).toHaveLength(0)
  })
})

describe('minimize and activate', () => {
  it('toggles the focused window from the taskbar', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    wm.activate(b)
    expect(wm.get(b)!.state).toBe('minimized')
    expect(wm.focusedId).toBe(a)
    wm.activate(b)
    expect(wm.get(b)!.state).toBe('normal')
    expect(wm.focusedId).toBe(b)
  })

  it('restores a minimized maximized window to maximized', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    wm.toggleMaximize(a)
    wm.minimize(a)
    expect(wm.focusedId).toBeNull()
    wm.focus(a)
    expect(wm.get(a)!.state).toBe('maximized')
  })
})

describe('close', () => {
  it('runs close handlers and focuses the next window', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    const handler = vi.fn()
    wm.onClose(b, handler)
    wm.close(b)
    expect(handler).toHaveBeenCalledOnce()
    expect(wm.get(b)).toBeUndefined()
    expect(wm.focusedId).toBe(a)
  })

  it('keeps the window when the close guard refuses', async () => {
    const wm = useWindows()
    const a = wm.open('files')!
    wm.setCloseGuard(a, () => false)
    await wm.requestClose(a)
    expect(wm.get(a)).toBeDefined()
    wm.setCloseGuard(a, async () => true)
    await wm.requestClose(a)
    expect(wm.get(a)).toBeUndefined()
  })
})

describe('modal dialogs', () => {
  it('keep the focus while open, and give it back when they close', () => {
    const wm = useWindows()
    const parent = wm.open('text-editor')!
    const other = wm.open('files')!
    const dialog = wm.openDialog(parent, 'file-chooser')!
    expect(wm.focusedId).toBe(dialog)
    expect(wm.get(dialog)!.z).toBeGreaterThan(wm.get(parent)!.z)
    expect(wm.get(parent)!.z).toBeGreaterThan(wm.get(other)!.z)
    expect(wm.ordered.map((w) => w.id)).toEqual([parent, other])
    expect(wm.openDialog(parent, 'file-chooser')).toBeUndefined()

    // Raising the parent (a click, the taskbar) raises the dialog above it.
    wm.focus(other)
    wm.focus(parent)
    expect(wm.focusedId).toBe(dialog)
    expect(wm.get(dialog)!.z).toBeGreaterThan(wm.get(parent)!.z)

    wm.close(dialog)
    expect(wm.focusedId).toBe(parent)
  })

  it('hide with their parent, which waits for them before it closes', async () => {
    const wm = useWindows()
    const other = wm.open('files')!
    const parent = wm.open('text-editor')!
    const dialog = wm.openDialog(parent, 'file-chooser')!
    wm.activate(parent)
    expect(wm.get(parent)!.state).toBe('minimized')
    expect(wm.isHidden(wm.get(dialog)!)).toBe(true)
    expect(wm.focusedId).toBe(other)
    wm.activate(parent)
    expect(wm.isHidden(wm.get(dialog)!)).toBe(false)
    expect(wm.focusedId).toBe(dialog)

    await wm.requestClose(parent)
    expect(wm.get(parent)).toBeDefined()
    wm.close(parent)
    expect(wm.get(dialog)).toBeUndefined()
  })

  it('are not saved with the layout', async () => {
    vi.useFakeTimers()
    const wm = useWindows()
    wm.load('alice')
    const parent = wm.open('text-editor')!
    wm.openDialog(parent, 'file-chooser')
    await nextTick()
    vi.advanceTimersByTime(1000)
    const saved = JSON.parse(localStorage.getItem('tilde:layout:alice')!) as { windows: unknown[]; focusedId: string }
    expect(saved.windows).toHaveLength(1)
    expect(saved.focusedId).toBe(parent)
  })
})

describe('setRect', () => {
  it('keeps the title bar reachable', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    wm.setRect(a, { x: -5000, y: -50, w: 400, h: 300 })
    const r = wm.get(a)!.rect
    expect(r.y).toBe(0)
    expect(r.x + r.w).toBeGreaterThan(0)
    wm.setRect(a, { x: 99999, y: 99999, w: 400, h: 300 })
    const r2 = wm.get(a)!.rect
    expect(r2.x).toBeLessThan(wm.area.w)
    expect(r2.y).toBeLessThan(wm.area.h)
  })
})

describe('layout persistence', () => {
  async function saveLayout(user: string, build: (wm: ReturnType<typeof useWindows>) => void) {
    vi.useFakeTimers()
    const wm = useWindows()
    wm.load(user)
    build(wm)
    await nextTick()
    vi.advanceTimersByTime(1000)
    vi.useRealTimers()
  }

  it('restores windows per user after a reload', async () => {
    let id = ''
    await saveLayout('alice', (wm) => {
      wm.open('files', { path: '/tmp' })
      id = wm.open('terminal')!
      wm.saveState(id, { terminalId: 't1' })
    })

    setActivePinia(createPinia())
    const wm = useWindows()
    wm.load('alice')
    expect(wm.windows.map((w) => w.appId)).toEqual(['files', 'terminal'])
    expect(wm.get(id)!.args).toEqual({ terminalId: 't1' })
    expect(wm.focusedId).toBe(id)

    wm.load('bob')
    expect(wm.windows).toHaveLength(0)
  })

  it('keeps a change made just before the layout is reset (lock, logout)', async () => {
    const wm = useWindows()
    wm.load('alice')
    const id = wm.open('terminal')!
    await nextTick()
    wm.saveState(id, { terminalId: 't1' })
    await nextTick()
    wm.reset()
    expect(localStorage.getItem('')).toBeNull()

    wm.load('alice')
    expect(wm.get(id)?.args).toEqual({ terminalId: 't1' })
  })

  it('drops unknown apps and other layout versions', () => {
    const win = {
      id: 'w1',
      appId: 'files',
      title: 'F',
      icon: 'x',
      rect: { x: 0, y: 0, w: 1, h: 1 },
      state: 'normal',
      z: 11,
      args: {},
    }
    localStorage.setItem(
      'tilde:layout:alice',
      JSON.stringify({ version: 1, windows: [win, { ...win, id: 'w2', appId: 'gone' }] }),
    )
    const wm = useWindows()
    wm.load('alice')
    expect(wm.windows.map((w) => w.id)).toEqual(['w1'])

    localStorage.setItem('tilde:layout:alice', JSON.stringify({ version: 999, windows: [win] }))
    wm.load('alice')
    expect(wm.windows).toHaveLength(0)
  })

  it('survives corrupt storage', () => {
    localStorage.setItem('tilde:layout:alice', '{not json')
    const wm = useWindows()
    wm.load('alice')
    expect(wm.windows).toHaveLength(0)
  })
})
