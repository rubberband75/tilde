import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useSwitcher } from './switcher'
import { useWindows } from './windows'

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('window switcher', () => {
  it('lists windows by last use and starts on the one before the current', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    const c = wm.open('system-info')!
    wm.focus(a)
    const s = useSwitcher()
    s.start()
    expect(s.ids).toEqual([a, c, b])
    expect(s.selectedId).toBe(c)
    s.step()
    s.step()
    expect(s.selectedId).toBe(a)
    s.step(true)
    s.commit()
    expect(s.active).toBe(false)
    expect(wm.focusedId).toBe(b)
  })

  it('starts on the last window going back, and on the first when none is on screen', () => {
    const wm = useWindows()
    const a = wm.open('files')!
    const b = wm.open('terminal')!
    const s = useSwitcher()
    s.start(true)
    expect(s.selectedId).toBe(a)
    s.cancel()
    wm.minimize(b)
    wm.minimize(a)
    s.start()
    // b was raised last.
    expect(s.selectedId).toBe(b)
    s.commit()
    expect(wm.get(b)!.state).toBe('normal')
  })

  it('leaves dialogs out: their parent brings them up', () => {
    const wm = useWindows()
    const parent = wm.open('text-editor')!
    const dialog = wm.openDialog(parent, 'file-chooser')!
    const other = wm.open('files')!
    const s = useSwitcher()
    s.start()
    expect(s.ids).toEqual([other, parent])
    s.commit()
    expect(wm.focusedId).toBe(dialog)
  })

  it('does nothing without windows', () => {
    const s = useSwitcher()
    s.start()
    expect(s.active).toBe(false)
  })
})
