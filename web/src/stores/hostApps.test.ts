import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { HostApp } from '@/api/types'
import { useHostApps } from './hostApps'

const list = vi.fn<() => Promise<{ available: boolean; apps: HostApp[] }>>()
vi.mock('@/api/hostApps', () => ({ hostAppsApi: { list: () => list() } }))

beforeEach(() => {
  setActivePinia(createPinia())
  list.mockReset()
})

describe('useHostApps', () => {
  it('loads the host apps once per opening, and keeps them when the worker cannot answer', async () => {
    const baobab: HostApp = { id: 'org.gnome.baobab', name: 'Disk Usage Analyzer', category: 'System Tools' }
    list.mockResolvedValue({ available: true, apps: [baobab] })
    const s = useHostApps()
    await Promise.all([s.load(), s.load()])
    expect(list).toHaveBeenCalledTimes(1)
    expect(s.available).toBe(true)
    expect(s.apps).toEqual([baobab])

    list.mockRejectedValue(new Error('down'))
    await s.load()
    expect(s.apps).toEqual([baobab])
  })
})
