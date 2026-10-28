import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { LockSettings, Settings } from '@/api/types'
import { defaultSettings, useSettings } from './settings'

const get = vi.fn<() => Promise<{ settings: Settings; desktopLock: LockSettings }>>()
const save = vi.fn<(s: Settings) => Promise<Settings>>()
vi.mock('@/api/settings', () => ({ settingsApi: { get: () => get(), save: (s: Settings) => save(s) } }))

beforeEach(() => {
  setActivePinia(createPinia())
  get.mockReset()
  save.mockReset()
})

describe('useSettings', () => {
  it('starts from the defaults and keeps them when the worker cannot answer', async () => {
    const s = useSettings()
    get.mockRejectedValue(new Error('down'))
    await s.load()
    expect(s.settings).toEqual(defaultSettings())
    expect(s.desktopLock).toBeNull()
  })

  it('loads the settings and what the desktop says', async () => {
    const s = useSettings()
    const saved = defaultSettings()
    saved.effects.animations = false
    get.mockResolvedValue({ settings: saved, desktopLock: { source: 'cinnamon', lockAfter: 600 } })
    await s.load()
    expect(s.settings.effects.animations).toBe(false)
    expect(s.desktopLock).toEqual({ source: 'cinnamon', lockAfter: 600 })
  })

  it('applies a change before the save answers, without touching the old settings', async () => {
    const s = useSettings()
    const before = s.settings
    let answer!: (v: Settings) => void
    save.mockImplementation(() => new Promise((r) => (answer = r)))
    const done = s.save((n) => (n.panel.network = false))
    expect(s.settings.panel.network).toBe(false)
    expect(before.panel.network).toBe(true)
    answer(save.mock.calls[0][0])
    await done
    expect(s.settings.panel.network).toBe(false)
  })

  it('puts the settings back when a save fails', async () => {
    const s = useSettings()
    save.mockRejectedValue(new Error('read-only'))
    await expect(s.save((n) => (n.lock = { own: true, enabled: true, minutes: 5 }))).rejects.toThrow('read-only')
    expect(s.settings).toEqual(defaultSettings())
  })
})
