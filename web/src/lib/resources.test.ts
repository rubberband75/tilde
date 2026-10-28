import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { Resources } from '@/api/types'
import { HISTORY, useResources } from '@/stores/resources'
import { resourcesWatched, setResourcesSender, watchResources } from './resources'

describe('resource watches', () => {
  it('tell the worker when the first starts and the last stops, and again after a reconnect', () => {
    const sent: boolean[] = []
    setResourcesSender((on) => sent.push(on))
    expect(sent).toEqual([])
    const a = watchResources()
    const b = watchResources()
    expect(sent).toEqual([true])
    a()
    a() // twice is harmless
    expect(sent).toEqual([true])
    expect(resourcesWatched()).toBe(true)

    // A new connection starts unwatched: the watch goes again.
    setResourcesSender((on) => sent.push(on))
    expect(sent).toEqual([true, true])
    b()
    expect(sent).toEqual([true, true, false])
    expect(resourcesWatched()).toBe(false)
    setResourcesSender(undefined)
  })
})

describe('resources store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  const snap = (t: number): Resources => ({
    time: t,
    cpu: t,
    cores: [],
    memory: { total: 1, used: 0 },
    swap: { total: 0, used: 0 },
    netReceive: 0,
    netSend: 0,
    netReceived: 0,
    netSent: 0,
    diskRead: 0,
    diskWrite: 0,
  })

  it('keeps a minute, starting from the worker’s history', () => {
    const res = useResources()
    res.setHistory([snap(1), snap(2)])
    for (let t = 3; t <= HISTORY + 5; t++) res.add(snap(t))
    expect(res.history).toHaveLength(HISTORY)
    expect(res.latest?.time).toBe(HISTORY + 5)
    expect(res.history[0]?.time).toBe(6)
    res.reset()
    expect(res.latest).toBeNull()
  })
})
