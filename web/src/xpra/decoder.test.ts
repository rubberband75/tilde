import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DrawDecoder } from './decoder'
import { parseDraw, type Draw } from './draw'

/** A worker that does nothing until the test answers for it. */
class FakeWorker {
  static all: FakeWorker[] = []
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  posted: { id: number }[] = []
  terminated = false
  constructor() {
    FakeWorker.all.push(this)
  }
  postMessage(m: { id: number }) {
    this.posted.push(m)
  }
  terminate() {
    this.terminated = true
  }
}

const draw = (): Draw => parseDraw(['draw', 1, 0, 0, 1, 1, 'png', new Uint8Array(4), 1, 0, {}])!

describe('DrawDecoder', () => {
  beforeEach(() => {
    FakeWorker.all = []
    vi.stubGlobal('Worker', FakeWorker)
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('answers each draw from the worker', async () => {
    const d = new DrawDecoder()
    const a = d.decode(draw())
    const b = d.decode(draw())
    const w = FakeWorker.all[0]!
    const bitmap = {} as ImageBitmap
    w.onmessage?.({ data: { id: w.posted[1]!.id, error: 'bad' } })
    w.onmessage?.({ data: { id: w.posted[0]!.id, bitmap } })
    await expect(a).resolves.toBe(bitmap)
    await expect(b).rejects.toThrow('bad')
    expect(FakeWorker.all).toHaveLength(1)
  })

  it('gives up on a worker that hangs, and starts another', async () => {
    const d = new DrawDecoder()
    const a = d.decode(draw())
    const failed = expect(a).rejects.toThrow('too long')
    vi.advanceTimersByTime(30_000)
    await failed
    expect(FakeWorker.all[0]?.terminated).toBe(true)
    void d.decode(draw()).catch(() => {})
    expect(FakeWorker.all).toHaveLength(2)
    d.close()
  })

  it('fails what was waiting when it closes', async () => {
    const d = new DrawDecoder()
    const a = d.decode(draw())
    d.close()
    await expect(a).rejects.toThrow('the connection ended')
  })
})
