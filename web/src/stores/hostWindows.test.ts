import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Transport } from '@/xpra/client'
import { encodePacket, PacketReader, type Packet } from '@/xpra/framing'
import type { Encodable } from '@/xpra/rencode'
import { useHostWindows } from './hostWindows'
import { useNotices } from './notices'
import { useWindows } from './windows'

const connect = vi.hoisted(() => vi.fn())
vi.mock('@/api/hostApps', () => ({ hostAppsApi: { connect } }))
const decode = vi.hoisted(() => vi.fn())
vi.mock('@/xpra/decoder', () => ({
  DrawDecoder: class {
    decode = decode
    close() {}
  },
}))

class FakeXpra implements Transport {
  onData: ((b: Uint8Array) => void) | null = null
  onClose: ((r: string) => void) | null = null
  closed = false
  private reader = new PacketReader()
  private got: Packet[] = []
  send(b: Uint8Array) {
    this.got.push(...this.reader.push(b.slice()))
  }
  close() {
    this.closed = true
  }
  sent(type?: string): Packet[] {
    return this.got.filter((p) => !type || p[0] === type)
  }
  packet(type: string, ...args: Encodable[]) {
    this.onData?.(encodePacket(type, args))
  }
}

/** A PNG's signature and header: all the store reads before decoding. */
function png(w: number, h: number): Uint8Array {
  const b = new Uint8Array(32)
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52])
  new DataView(b.buffer).setUint32(16, w)
  new DataView(b.buffer).setUint32(20, h)
  return b
}

const context = { drawImage: vi.fn() }
const bitmap = () => ({ close: vi.fn(), width: 16, height: 16 }) as unknown as ImageBitmap
const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve()
}

describe('host windows', () => {
  let xpra: FakeXpra

  beforeEach(async () => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.useFakeTimers({ toFake: ['setTimeout', 'setInterval', 'clearTimeout', 'clearInterval'] })
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as never)
    vi.stubGlobal(
      'createImageBitmap',
      vi.fn(async () => bitmap()),
    )
    context.drawImage.mockClear()
    decode.mockReset()
    decode.mockImplementation(async () => bitmap())
    xpra = new FakeXpra()
    connect.mockReset()
    connect.mockResolvedValue(xpra)
    await useHostWindows().connect()
    xpra.packet('hello', { version: '6.5' })
  })
  afterEach(() => {
    useHostWindows().disconnect()
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  const opened = () => useWindows().windows.filter((w) => w.appId === 'host-window')

  it('says hello as a client that shows windows', () => {
    const [hello] = xpra.sent('hello')
    expect(hello?.[1]).toMatchObject({ ui_client: true, windows: true, steal: true, keyboard: false })
    expect((hello?.[1] as { encoding: object }).encoding).toMatchObject({ transparency: false, rgb_lz4: true })
  })

  it('makes each new window a Tilde window, around the content, and maps it', () => {
    xpra.packet('new-window', 5, 300, 200, 400, 250, { title: 'xterm' }, {})
    const [w] = opened()
    expect(w).toMatchObject({ title: 'xterm', args: { wid: 5 } })
    // 1px of border, and the title bar above: the content is where the app said.
    expect(w?.rect).toMatchObject({ x: 299, w: 402 })
    expect(w!.rect.y + w!.rect.h - 250 - 1).toBe(200)
    expect(xpra.sent('map-window')).toEqual([['map-window', 5, 300, 200, 400, 250, {}]])
    expect(useHostWindows().canvasOf(5)).toMatchObject({ width: 400, height: 250 })
  })

  it('places a window with no position of its own as any other, and says where', () => {
    xpra.packet('new-window', 5, 0, 0, 400, 250, {}, {})
    const [w] = opened()
    expect(xpra.sent('map-window')[0]?.slice(2, 4)).toEqual([w!.rect.x + 1, w!.rect.y + w!.rect.h - 250 - 1])
  })

  it('ignores windows it cannot show', () => {
    xpra.packet('new-window', 1, 0, 0, 0, 10, {}, {})
    xpra.packet('new-window', 2, 0, 0, 100000, 10, {}, {})
    xpra.packet('new-window', 'x', 0, 0, 10, 10, {}, {})
    xpra.packet('new-window', 3, 0, 0, 10, 10, {}, {})
    xpra.packet('new-window', 3, 0, 0, 10, 10, {}, {})
    expect(opened()).toHaveLength(1)
    for (let i = 10; i < 100; i++) xpra.packet('new-window', i, 0, 0, 10, 10, {}, {})
    expect(opened()).toHaveLength(64)
  })

  it('follows the title, as text of a bounded length', () => {
    xpra.packet('new-window', 5, 0, 0, 10, 10, { title: 'one' }, {})
    xpra.packet('window-metadata', 5, { title: 'x'.repeat(1000) })
    expect(opened()[0]?.title).toHaveLength(512)
    xpra.packet('window-metadata', 5, { title: 7 })
    xpra.packet('window-metadata', 99, { title: 'no such window' })
    expect(opened()[0]?.title).toHaveLength(512)
  })

  it('shows an icon only if it is a picture, of a sensible size', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:icon')
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    xpra.packet('window-icon', 5, 16, 16, 'svg', png(16, 16))
    xpra.packet('window-icon', 5, 9999, 16, 'png', png(16, 16))
    // It says 16×16; its own header says otherwise: not decoded at all.
    xpra.packet('window-icon', 5, 16, 16, 'png', png(30000, 30000))
    await flush()
    expect(createImageBitmap).not.toHaveBeenCalled()
    expect(opened()[0]?.iconSrc).toBeUndefined()
    vi.mocked(createImageBitmap).mockRejectedValueOnce(new Error('not an image'))
    xpra.packet('window-icon', 5, 16, 16, 'png', png(16, 16))
    await flush()
    expect(opened()[0]?.iconSrc).toBeUndefined()
    xpra.packet('window-icon', 5, 16, 16, 'png', png(16, 16))
    await flush()
    expect(opened()[0]?.iconSrc).toBe('blob:icon')
  })

  it('gives back an icon when it changes, and when the window goes', async () => {
    let n = 0
    URL.createObjectURL = vi.fn(() => `blob:${++n}`)
    URL.revokeObjectURL = vi.fn()
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    xpra.packet('window-icon', 5, 16, 16, 'png', png(16, 16))
    await flush()
    xpra.packet('window-icon', 5, 16, 16, 'png', png(16, 16))
    await flush()
    expect(opened()[0]?.iconSrc).toBe('blob:2')
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenLastCalledWith('blob:1')
    xpra.packet('lost-window', 5)
    expect(URL.revokeObjectURL).toHaveBeenLastCalledWith('blob:2')
  })

  it('keeps the canvases within a budget of pixels, and gives them back', () => {
    xpra.packet('new-window', 1, 0, 0, 8192, 8192, {}, {}) // over one window's share
    expect(opened()).toHaveLength(0)
    for (let i = 1; i <= 5; i++) xpra.packet('new-window', i, 0, 0, 8000, 8000, {}, {})
    expect(opened()).toHaveLength(4) // 64 million each, of 256 million
    const canvas = useHostWindows().canvasOf(1)!
    xpra.packet('lost-window', 1)
    expect(canvas.width).toBe(0)
    xpra.packet('new-window', 6, 0, 0, 8000, 8000, {}, {})
    expect(opened()).toHaveLength(4)
  })

  describe('drawing', () => {
    beforeEach(() => xpra.packet('new-window', 5, 0, 0, 100, 100, {}, {}))

    it('paints, then acknowledges, or Xpra stops sending', async () => {
      xpra.packet('draw', 5, 1, 2, 3, 4, 'png', png(3, 4), 42, 0, {})
      await flush()
      expect(context.drawImage).toHaveBeenCalledWith(expect.anything(), 1, 2, 3, 4)
      const [ack] = xpra.sent('damage-sequence')
      expect(ack?.slice(1, 5)).toEqual([42, 5, 3, 4])
      expect(ack?.[5]).toBeGreaterThanOrEqual(0)
    })

    it('paints in order, however the decoding goes', async () => {
      let finish = (_: ImageBitmap) => {}
      const first = bitmap()
      decode.mockReturnValueOnce(new Promise((r) => (finish = r)))
      xpra.packet('draw', 5, 0, 0, 1, 1, 'png', png(1, 1), 1, 0, {})
      xpra.packet('draw', 5, 0, 0, 10, 10, 'scroll', '', 2, 0, { scroll: [[0, 10, 10, 10, 0, -10]] })
      await flush()
      expect(context.drawImage).not.toHaveBeenCalled()
      finish(first)
      await flush()
      expect(context.drawImage.mock.calls[0]?.[0]).toBe(first)
      expect(context.drawImage.mock.calls[1]?.slice(1)).toEqual([0, 10, 10, 10, 0, 0, 10, 10])
      expect(xpra.sent('damage-sequence').map((p) => p[1])).toEqual([1, 2])
    })

    it('says so when a paint fails, and goes on', async () => {
      decode.mockRejectedValueOnce(new Error('bad image'))
      xpra.packet('draw', 5, 0, 0, 1, 1, 'png', png(1, 1), 1, 0, {})
      xpra.packet('draw', 5, 0, 0, 1, 1, 'nonsense', '', 2, 0, {})
      xpra.packet('draw', 5, 0, 0, 1, 1, 'void', '', 3, 0, {})
      await flush()
      const acks = xpra.sent('damage-sequence')
      expect(acks.map((p) => [p[1], p[5], p[6]])).toEqual([
        [1, -1, 'bad image'],
        [2, -1, 'unsupported encoding'],
        [3, expect.any(Number), ''],
      ])
      expect(acks[2]?.[5]).toBeGreaterThanOrEqual(0)
    })

    it.each<[string, Encodable[]]>([
      ['outside the window', [50, 50, 100, 100, 'png', png(100, 100)]],
      ['at a negative place', [-1, 0, 10, 10, 'png', png(10, 10)]],
      ['an image bigger than it says', [0, 0, 10, 10, 'png', png(8000, 8000)]],
      ['not an image', [0, 0, 10, 10, 'png', new Uint8Array(40)]],
      ['a scroll out of the window', [0, 0, 10, 10, 'scroll', [[0, 0, 500, 500, 0, 0]]]],
    ])('refuses %s without decoding it', async (_, args) => {
      xpra.packet('draw', 5, ...args, 1, 0, {})
      await flush()
      expect(decode).not.toHaveBeenCalled()
      expect(context.drawImage).not.toHaveBeenCalled()
      expect(xpra.sent('damage-sequence')[0]?.[5]).toBe(-1)
    })

    it('ends the connection when draws pile up beyond any flow control', () => {
      decode.mockReturnValue(new Promise(() => {}))
      for (let i = 0; i < 256; i++) xpra.packet('draw', 5, 0, 0, 1, 1, 'png', png(1, 1), i, 0, {})
      expect(xpra.closed).toBe(false)
      xpra.packet('draw', 5, 0, 0, 1, 1, 'png', png(1, 1), 256, 0, {})
      expect(xpra.closed).toBe(true)
      expect(decode).toHaveBeenCalledTimes(256)
    })

    it('acknowledges what it does not show', () => {
      xpra.packet('draw', 77, 0, 0, 1, 1, 'png', new Uint8Array(4), 9, 0, {})
      expect(decode).not.toHaveBeenCalled()
      expect(xpra.sent('damage-sequence')[0]?.slice(1, 6)).toEqual([9, 77, 1, 1, 0])
    })

    it('ends the connection on a draw it cannot read', () => {
      xpra.packet('draw', 5, 'x')
      expect(xpra.sent('disconnect')).toHaveLength(1)
      expect(opened()).toHaveLength(0)
    })
  })

  it('asks the app to close; the window goes when Xpra says so', () => {
    const host = useHostWindows()
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    expect(host.requestClose(5)).toBe(false)
    expect(xpra.sent('close-window')).toEqual([['close-window', 5]])
    expect(opened()).toHaveLength(1)
    xpra.packet('lost-window', 5)
    expect(opened()).toHaveLength(0)
    expect(host.requestClose(5)).toBe(true)
  })

  it('does not save host windows with the layout', async () => {
    const wm = useWindows()
    wm.load('me')
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    wm.open('files')
    await flush()
    vi.advanceTimersByTime(1000)
    const saved = JSON.parse(localStorage.getItem('tilde:layout:me') ?? '{}') as { windows: { appId: string }[] }
    expect(saved.windows.map((w) => w.appId)).toEqual(['files'])
  })

  it('closes the Host Apps window: both cannot have the session', async () => {
    const host = useHostWindows()
    host.disconnect()
    const wm = useWindows()
    wm.open('xpra')
    xpra = new FakeXpra()
    connect.mockResolvedValue(xpra)
    await host.connect()
    expect(wm.windows.filter((w) => w.appId === 'xpra')).toHaveLength(0)
  })

  it('on losing the session, closes its windows, says why and does not reconnect', async () => {
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    xpra.packet('disconnect', 'new client')
    await flush()
    expect(opened()).toHaveLength(0)
    expect(useNotices().items.at(-1)?.message).toBe('Host apps are now open in another browser or window.')
    expect(connect).toHaveBeenCalledTimes(1)
  })

  it('shows why it ended as short, plain text', async () => {
    xpra.packet('disconnect', `stop\u202Enow\n${'x'.repeat(5000)}`)
    await flush()
    const m = useNotices().items.at(-1)?.message ?? ''
    expect(m.startsWith('Host apps disconnected: stop now ')).toBe(true)
    expect(m.length).toBeLessThan(250)
  })

  it('leaving quietly closes the windows and says nothing', () => {
    xpra.packet('new-window', 5, 0, 0, 10, 10, {}, {})
    useHostWindows().disconnect()
    expect(opened()).toHaveLength(0)
    expect(xpra.sent('disconnect')).toHaveLength(1)
    expect(useNotices().items).toHaveLength(0)
  })

  it('drops a connection still being made when it leaves', async () => {
    const host = useHostWindows()
    host.disconnect()
    const late = new FakeXpra()
    let finish = (_: Transport) => {}
    connect.mockReturnValue(new Promise((r) => (finish = r)))
    const p = host.connect()
    host.disconnect()
    finish(late)
    await p
    expect(late.closed).toBe(true)
    expect(late.sent()).toEqual([])
  })

  it('connects again straight after leaving, not with the attempt it dropped', async () => {
    const host = useHostWindows()
    host.disconnect()
    let finish = (_: Transport) => {}
    connect.mockReturnValueOnce(new Promise((r) => (finish = r)))
    const dropped = host.connect()
    host.disconnect()
    const fresh = new FakeXpra()
    connect.mockResolvedValueOnce(fresh)
    await host.connect()
    expect(fresh.sent('hello')).toHaveLength(1)
    finish(new FakeXpra())
    await dropped
    await flush()
    expect(fresh.closed).toBe(false)
  })
})
