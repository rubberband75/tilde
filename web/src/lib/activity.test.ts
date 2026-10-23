import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createReporter, trackInput } from './activity'

describe('createReporter', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('reports the first input at once, then at most once per heartbeat', async () => {
    const send = vi.fn(() => Promise.resolve({ lockAfter: 600, heartbeat: 15 }))
    const r = createReporter(send, 15)

    r.input()
    expect(send).toHaveBeenCalledTimes(1)
    for (let i = 0; i < 10; i++) {
      r.input()
      await vi.advanceTimersByTimeAsync(1000)
    }
    expect(send).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5000)
    expect(send).toHaveBeenCalledTimes(2)

    // Quiet: nothing is sent without input.
    await vi.advanceTimersByTimeAsync(60_000)
    expect(send).toHaveBeenCalledTimes(2)
    r.input()
    expect(send).toHaveBeenCalledTimes(3)
  })

  it('follows the heartbeat the server answers with', async () => {
    const send = vi.fn(() => Promise.resolve({ lockAfter: 10, heartbeat: 1 }))
    const r = createReporter(send, 15)
    r.input()
    await vi.advanceTimersByTimeAsync(0)
    r.input()
    await vi.advanceTimersByTimeAsync(1000)
    expect(send).toHaveBeenCalledTimes(2)
  })

  it('sends nothing once stopped, even a report already scheduled', async () => {
    const send = vi.fn(() => Promise.resolve({ lockAfter: 600, heartbeat: 15 }))
    const r = createReporter(send, 15)
    r.input()
    r.input()
    r.stop()
    await vi.advanceTimersByTimeAsync(30_000)
    r.input()
    expect(send).toHaveBeenCalledTimes(1)
  })

  it('survives a failed report', async () => {
    const send = vi.fn(() => Promise.reject(new Error('locked')))
    const r = createReporter(send, 1)
    r.input()
    await vi.advanceTimersByTimeAsync(1000)
    r.input()
    expect(send).toHaveBeenCalledTimes(2)
  })
})

describe('trackInput', () => {
  it('counts keys and pointer input anywhere until removed', () => {
    const input = vi.fn()
    const stop = vi.fn()
    const remove = trackInput({ input, stop })
    document.body.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true }))
    window.dispatchEvent(new Event('pointermove'))
    expect(input).toHaveBeenCalledTimes(2)
    remove()
    window.dispatchEvent(new Event('pointerdown'))
    expect(input).toHaveBeenCalledTimes(2)
    expect(stop).toHaveBeenCalled()
  })
})
