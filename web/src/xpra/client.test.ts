import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { helloCaps, MAX_MESSAGE, XpraClient, type Transport } from './client'
import { encodePacket, PacketReader, type Packet } from './framing'
import type { Encodable } from './rencode'

/** A transport whose far end is the test: it reads what the client sends. */
class FakeXpra implements Transport {
  onData: ((bytes: Uint8Array) => void) | null = null
  onClose: ((reason: string) => void) | null = null
  closed = false
  messages: Uint8Array[] = []
  private reader = new PacketReader()
  private got: Packet[] = []

  send(bytes: Uint8Array) {
    this.messages.push(bytes)
    this.got.push(...this.reader.push(bytes.slice()))
  }
  close() {
    this.closed = true
  }
  /** What the client sent since last asked. */
  sent(): Packet[] {
    return this.got.splice(0)
  }
  /** Sends the client a packet, as Xpra would. */
  packet(type: string, ...args: Encodable[]) {
    this.onData?.(encodePacket(type, args))
  }
}

describe('XpraClient', () => {
  let xpra: FakeXpra
  let client: XpraClient
  let now: number
  let closed: string[]
  let packets: Packet[]

  beforeEach(() => {
    vi.useFakeTimers()
    now = 1000
    xpra = new FakeXpra()
    client = new XpraClient(xpra, { caps: helloCaps({ uuid: 'u1', ui: false }), now: () => now })
    closed = []
    packets = []
    client.onClose = (r) => closed.push(r)
    client.onPacket = (p) => packets.push(p)
    client.start()
  })
  afterEach(() => vi.useRealTimers())

  it('says hello as a rencodeplus client with LZ4, and waits for the answer', () => {
    const [hello] = xpra.sent()
    expect(hello?.[0]).toBe('hello')
    expect(hello?.[1]).toMatchObject({
      version: '6.0',
      client_type: 'Tilde',
      uuid: 'u1',
      ui_client: false,
      steal: true,
    })
    expect(hello?.[1]).toMatchObject({ rencodeplus: true, lz4: true, brotli: false })
    expect(client.state).toBe('connecting')

    let server: unknown
    client.onHello = (caps) => (server = caps)
    xpra.packet('hello', { version: '6.5.5', 'packet-types': ['ping', 'ping_echo'] })
    expect(client.state).toBe('connected')
    expect(server).toEqual({ version: '6.5.5', 'packet-types': ['ping', 'ping_echo'] })
    expect(client.server?.version).toBe('6.5.5')
  })

  it('gives up without an answer', () => {
    vi.advanceTimersByTime(30_000)
    expect(client.state).toBe('closed')
    expect(closed).toEqual(['no answer from Xpra'])
    expect(xpra.sent().map((p) => p[0])).toEqual(['hello', 'disconnect'])
    expect(xpra.closed).toBe(true)
  })

  describe('connected', () => {
    let afterHello: Packet[]
    beforeEach(() => {
      xpra.sent()
      xpra.packet('hello', { version: '6.5.5' })
      afterHello = xpra.sent()
    })

    it('pings at once, then every five seconds, and measures the round trip', () => {
      expect(afterHello).toEqual([['ping', 1000]])
      now = 1040
      xpra.packet('ping_echo', 1000, 0, 0, 0, -1, '')
      expect(client.latency).toBe(40)
      now = 6000
      vi.advanceTimersByTime(5000)
      expect(xpra.sent()).toEqual([['ping', 6000]])
    })

    it('ends when pings go unanswered', () => {
      xpra.packet('ping_echo', 1000, 0, 0, 0, -1, '')
      now = 6000
      vi.advanceTimersByTime(5000) // pings at 6000; nothing comes back
      vi.advanceTimersByTime(14_999)
      expect(client.state).toBe('connected')
      vi.advanceTimersByTime(1)
      expect(closed).toEqual(['Xpra stopped answering'])
    })

    it("echoes the server's pings", () => {
      client.latency = 12
      xpra.packet('ping', 777, 1700000000000)
      expect(xpra.sent()).toContainEqual(['ping_echo', 777, 0, 0, 0, 12, ''])
    })

    it('hands other packets on, by their Xpra 6 names', () => {
      xpra.packet('startup-complete')
      xpra.packet('window-create', 1, 0, 0, 100, 50, {}, {})
      expect(packets).toEqual([['startup-complete'], ['new-window', 1, 0, 0, 100, 50, {}, {}]])
    })

    it('ends when Xpra says goodbye, with its reason', () => {
      xpra.packet('disconnect', 'new client', 'this client has been replaced')
      expect(client.state).toBe('closed')
      expect(closed).toEqual(['new client: this client has been replaced'])
      expect(xpra.closed).toBe(true)
      xpra.packet('startup-complete')
      expect(packets).toEqual([])
    })

    it('ends when a packet cannot be handled', () => {
      client.onPacket = () => {
        throw new Error('no such window')
      }
      xpra.packet('lost-window', 7)
      expect(closed).toEqual(['could not handle lost-window: no such window'])
    })

    it('ends on a broken stream', () => {
      xpra.onData?.(new TextEncoder().encode('GET / HTTP/1.1\r\n'))
      expect(closed[0]).toMatch(/^protocol error/)
      expect(xpra.sent().map((p) => p[0])).toEqual(['disconnect'])
    })

    it('ends when the connection does, once', () => {
      xpra.onClose?.('the session ended')
      client.close('again')
      expect(closed).toEqual(['the session ended'])
      expect(client.closeReason).toBe('the session ended')
      vi.advanceTimersByTime(60_000)
      expect(xpra.sent()).toEqual([])
    })

    it('splits what it sends into messages the worker takes', () => {
      xpra.messages = []
      client.send('clipboard-contents', new Uint8Array(MAX_MESSAGE * 2 + 10))
      expect(xpra.messages.map((m) => m.length)).toEqual([MAX_MESSAGE, MAX_MESSAGE, 10 + 8 + 2 + 18 + 7])
      expect(xpra.sent()[0]?.[0]).toBe('clipboard-contents')
    })
  })
})
