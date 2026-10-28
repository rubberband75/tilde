import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useShell } from '@/stores/shell'
import { useSwitcher } from '@/stores/switcher'
import { useWindows } from '@/stores/windows'
import { installShortcuts } from './keyboard'

vi.mock('./actions', () => ({ lockScreen: vi.fn() }))

let remove: () => void
let appSaw: string[]
const appListener = (e: KeyboardEvent) => appSaw.push(`${e.type}:${e.code || e.key}`)

beforeEach(() => {
  setActivePinia(createPinia())
  remove = installShortcuts()
  appSaw = []
  // An app's own handler, as the terminal has, registered after the shell's.
  window.addEventListener('keydown', appListener, true)
})

afterEach(() => {
  remove()
  window.removeEventListener('keydown', appListener, true)
})

function press(type: 'keydown' | 'keyup', init: KeyboardEventInit) {
  const e = new KeyboardEvent(type, { bubbles: true, cancelable: true, ...init })
  document.body.dispatchEvent(e)
  return e
}

describe('shortcuts', () => {
  it('tile the focused window, and the app never sees the key', () => {
    const wm = useWindows()
    const id = wm.open('files')!
    const e = press('keydown', { code: 'ArrowLeft', key: 'ArrowLeft', altKey: true, shiftKey: true })
    expect(e.defaultPrevented).toBe(true)
    expect(wm.get(id)!.tile).toBe('left')
    expect(appSaw).toEqual([])

    press('keydown', { code: 'KeyA', key: 'a' })
    expect(appSaw).toEqual(['keydown:KeyA'])
  })

  it('switch windows while Alt is held, and pick one when it is let go', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    const c = wm.open('system-info')!
    const switcher = useSwitcher()

    press('keydown', { code: 'Backquote', key: '`', altKey: true })
    expect(switcher.selectedId).toBe(b)
    press('keydown', { code: 'Backquote', key: '`', altKey: true })
    expect(switcher.selectedId).toBe(a)
    press('keydown', { code: 'Backquote', key: '`', altKey: true, shiftKey: true })
    expect(switcher.selectedId).toBe(b)
    // Letting go of Shift keeps the switcher up; Alt picks the window.
    press('keyup', { code: 'ShiftLeft', key: 'Shift', altKey: true })
    expect(switcher.active).toBe(true)
    press('keyup', { code: 'AltLeft', key: 'Alt' })
    expect(switcher.active).toBe(false)
    expect(wm.focusedId).toBe(b)
    expect(appSaw).toEqual([])

    // Escape leaves the focus where it was.
    press('keydown', { code: 'Tab', key: 'Tab', altKey: true })
    press('keydown', { code: 'Escape', key: 'Escape', altKey: true })
    press('keyup', { code: 'AltLeft', key: 'Alt' })
    expect(wm.focusedId).toBe(b)
    void c
  })

  it('open the menu with Alt+F1, and with Super alone only when every key comes here', () => {
    const shell = useShell()
    press('keydown', { code: 'F1', key: 'F1', altKey: true })
    expect(shell.menuOpen).toBe(true)
    press('keydown', { code: 'F1', key: 'F1', altKey: true })
    expect(shell.menuOpen).toBe(false)

    // Outside fullscreen the client's own system menu takes Super.
    press('keydown', { code: 'MetaLeft', key: 'Meta', metaKey: true })
    press('keyup', { code: 'MetaLeft', key: 'Meta' })
    expect(shell.menuOpen).toBe(false)
  })

  it('ask the focused frame for its menu, and close the window', async () => {
    const wm = useWindows()
    const id = wm.open('files')!
    press('keydown', { code: 'Space', key: ' ', altKey: true, shiftKey: true })
    expect(wm.frameRequest).toEqual({ id, action: 'menu' })
    press('keydown', { code: 'F4', key: 'F4', altKey: true, shiftKey: true })
    await vi.waitFor(() => expect(wm.get(id)).toBeUndefined())
  })
})
