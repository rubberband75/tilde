import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SoundChange } from '@/api/sound'
import type { SoundState } from '@/api/types'
import { useSound } from './sound'

const state = vi.fn<() => Promise<SoundState>>()
const set = vi.fn<(c: SoundChange) => Promise<SoundState>>()
vi.mock('@/api/sound', () => ({ soundApi: { state: () => state(), set: (c: SoundChange) => set(c) } }))

const two: SoundState = {
  available: true,
  outputs: [
    { name: 'speakers', description: 'Speakers', volume: 50, muted: false, default: true },
    { name: 'hdmi', description: 'HDMI', volume: 100, muted: false, default: false },
  ],
  apps: [{ id: 7, name: 'Firefox', volume: 80, muted: false }],
}

beforeEach(() => {
  setActivePinia(createPinia())
  state.mockReset()
  set.mockReset()
})

describe('useSound', () => {
  it('shows a change at once, and a drag sends its latest volume, not each step', async () => {
    state.mockResolvedValue(two)
    const s = useSound()
    await s.refresh()
    expect(s.output?.name).toBe('speakers')
    let answer!: (v: SoundState) => void
    set.mockImplementation(() => new Promise((r) => (answer = r)))
    s.set({ output: 'speakers', volume: 51 })
    s.set({ output: 'speakers', volume: 52 })
    s.set({ output: 'speakers', volume: 60 })
    expect(s.output?.volume).toBe(60)
    expect(set).toHaveBeenCalledTimes(1)
    answer(two)
    await vi.waitFor(() => expect(set).toHaveBeenCalledTimes(2))
    expect(set.mock.calls[1][0]).toEqual({ output: 'speakers', volume: 60 })
  })

  it('makes another output the default, and keeps changes to different targets', async () => {
    state.mockResolvedValue(two)
    const s = useSound()
    await s.refresh()
    set.mockResolvedValue(two)
    s.set({ output: 'hdmi', default: true })
    s.set({ app: 7, muted: true })
    expect(s.output?.name).toBe('hdmi')
    expect(s.state.apps[0].muted).toBe(true)
    await vi.waitFor(() => expect(set).toHaveBeenCalledTimes(2))
    expect(set.mock.calls.map((c) => c[0])).toEqual([
      { output: 'hdmi', default: true },
      { app: 7, muted: true },
    ])
  })
})
