// Tilde's client of Xpra's protocol (D80, docs/xpra-client.md): the
// handshake, pings both ways, and packets in and out. It knows nothing of
// windows or the page: the host windows store gives it its capabilities
// and takes its packets, and tests drive it with a fake transport.

import { encodePacket, PacketReader, type Limits, type Packet } from './framing'
import type { Dict, Encodable, EncodableDict } from './rencode'
import { packetName } from './packets'

/** A byte stream to Xpra: the worker's WebSocket, or a test's. */
export interface Transport {
  send(bytes: Uint8Array): void
  close(): void
  /** Set by the client. */
  onData: ((bytes: Uint8Array) => void) | null
  /** Set by the client; called once, however the stream ended. */
  onClose: ((reason: string) => void) | null
}

/**
 * What Tilde sends in the worker's messages, at most: the relay takes up
 * to 1 MiB (xpraMessageLimit), and clipboard contents can be larger.
 */
export const MAX_MESSAGE = 256 << 10

/** The protocol Tilde speaks, for Xpra's version check (3.0 and later). */
export const PROTOCOL_VERSION = '6.0'

export interface HelloOptions {
  /** This browser's, so a reconnect replaces its old connection. */
  uuid: string
  /**
   * A client that shows windows. Xpra drives one at a time: a new one
   * takes the session over from the last. One that does not (a check of
   * the session) leaves the others alone.
   */
  ui: boolean
}

/** The capabilities every hello has; the windows store adds its own. */
export function helloCaps(o: HelloOptions): EncodableDict {
  return {
    version: PROTOCOL_VERSION,
    client_type: 'Tilde',
    uuid: o.uuid,
    ui_client: o.ui,
    // Take the session from another client rather than be refused.
    steal: true,
    share: false,
    rencodeplus: true,
    lz4: true,
    brotli: false,
    // Xpra's defaults but "aliases", which would number packet types.
    wants: ['versions', 'features', 'display', 'encodings', 'packet-types'],
    network: { pings: 5 },
  }
}

export interface ClientOptions {
  caps: EncodableDict
  limits?: Limits
  /** Milliseconds between pings, and without an answer before giving up. */
  pingEvery?: number
  pingTimeout?: number
  /** Milliseconds to wait for the server's hello. */
  helloTimeout?: number
  /** A monotonic clock in milliseconds. */
  now?: () => number
}

export type ClientState = 'connecting' | 'connected' | 'closed'

export class XpraClient {
  state: ClientState = 'connecting'
  /** The server's hello, once it came. */
  server: Dict | null = null
  /** The last ping's round trip, in milliseconds. */
  latency: number | null = null
  /** Why the connection ended. */
  closeReason = ''

  get closed(): boolean {
    return this.state === 'closed'
  }

  onHello: ((server: Dict) => void) | null = null
  /** Every packet but the handshake's and pings, by its Xpra 6 name. */
  onPacket: ((packet: Packet) => void) | null = null
  onClose: ((reason: string) => void) | null = null

  private readonly reader: PacketReader
  private readonly now: () => number
  private timers: ReturnType<typeof setTimeout>[] = []
  private pinger: ReturnType<typeof setInterval> | undefined
  private lastEcho = -Infinity

  constructor(
    private readonly transport: Transport,
    private readonly opts: ClientOptions,
  ) {
    this.reader = new PacketReader(opts.limits)
    this.now = opts.now ?? (() => performance.now())
    transport.onData = (b) => this.receive(b)
    transport.onClose = (reason) => this.ended(reason)
  }

  /** Sends the hello; the server answers with its own. */
  start() {
    this.send('hello', this.opts.caps)
    this.later(this.opts.helloTimeout ?? 30_000, () => {
      if (this.state === 'connecting') this.close('no answer from Xpra')
    })
  }

  send(type: string, ...args: Encodable[]) {
    if (this.closed) return
    const b = encodePacket(type, args)
    for (let at = 0; at < b.length; at += MAX_MESSAGE) this.transport.send(b.subarray(at, at + MAX_MESSAGE))
  }

  /** Says goodbye to Xpra and ends the connection. */
  close(reason: string) {
    if (this.closed) return
    this.send('disconnect', reason)
    this.ended(reason)
    this.transport.close()
  }

  private receive(bytes: Uint8Array) {
    if (this.closed) return
    let packets: Packet[]
    try {
      packets = this.reader.push(bytes)
    } catch (e) {
      this.close(`protocol error: ${(e as Error).message}`)
      return
    }
    for (const p of packets) {
      if (this.closed) return
      try {
        this.handle(p)
      } catch (e) {
        // Not left half-handled: the next packets may depend on it.
        this.close(`could not handle ${p[0]}: ${(e as Error).message}`)
      }
    }
  }

  private handle(p: Packet) {
    const type = packetName(p[0])
    switch (type) {
      case 'hello': {
        if (this.state !== 'connecting') return
        const caps = p[1]
        if (!caps || typeof caps !== 'object' || Array.isArray(caps) || caps instanceof Uint8Array) {
          this.close('a hello without capabilities')
          return
        }
        this.server = caps
        this.state = 'connected'
        this.startPings()
        this.onHello?.(caps)
        return
      }
      case 'ping': {
        // Echo the server's time; no load average, latency if known.
        const time = typeof p[1] === 'number' ? p[1] : 0
        const sid = typeof p[3] === 'string' ? p[3] : ''
        this.send('ping_echo', time, 0, 0, 0, Math.round(this.latency ?? -1), sid)
        return
      }
      case 'ping_echo': {
        const sent = p[1]
        if (typeof sent === 'number') {
          this.lastEcho = Math.max(this.lastEcho, sent)
          this.latency = Math.max(0, this.now() - sent)
        }
        return
      }
      case 'disconnect': {
        const why = p.slice(1).filter((s) => typeof s === 'string')
        this.ended(why.join(': ') || 'disconnected by Xpra')
        this.transport.close()
        return
      }
    }
    this.onPacket?.([type, ...p.slice(1)])
  }

  private startPings() {
    const every = this.opts.pingEvery ?? 5_000
    const timeout = this.opts.pingTimeout ?? 15_000
    const ping = () => {
      const t = Math.round(this.now())
      this.send('ping', t)
      this.later(timeout, () => {
        if (this.lastEcho < t) this.close('Xpra stopped answering')
      })
    }
    ping()
    this.pinger = setInterval(ping, every)
  }

  private later(ms: number, fn: () => void) {
    const t = setTimeout(() => {
      this.timers = this.timers.filter((x) => x !== t)
      fn()
    }, ms)
    this.timers.push(t)
  }

  private ended(reason: string) {
    if (this.closed) return
    this.state = 'closed'
    this.closeReason = reason
    clearInterval(this.pinger)
    for (const t of this.timers) clearTimeout(t)
    this.timers = []
    this.transport.onData = null
    this.transport.onClose = null
    this.onClose?.(reason)
  }
}
