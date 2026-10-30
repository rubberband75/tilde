// Xpra's packets on the wire (D80), from xpra/net/protocol/header.py and
// socket_handler.py. Each piece has an 8-byte header:
//
//   'P'  flags  compression  chunk  length (4 bytes, big-endian)
//
// flags name the encoding (0x10: rencodeplus, all Xpra 6 has); 0x08 is a
// hint to flush, 0x02 encryption, which Tilde does not use (the socket is
// the user's own). compression is a level in the low bits and LZ4 (0x10)
// or Brotli (0x40) in the high ones; Tilde asks for LZ4 only. A large
// item of a packet (pixels, an icon) may come first as a chunk of its own,
// numbered by its place in the packet, and is put back there when the
// packet itself (chunk 0) comes.
//
// The browser gets Xpra's bytes as they come, in pieces of any size: the
// worker relays the stream, not packets.

import { decode, encode, type Encodable, type Value } from './rencode'
import { lz4Decode } from './lz4'

export const HEADER_SIZE = 8
const MAGIC = 0x50 // 'P'
export const FLAG_RENCODEPLUS = 0x10
const FLAG_CIPHER = 0x02
const FLAG_FLUSH = 0x08
const COMPRESS_LZ4 = 0x10
/** Xpra's own limits: no more than this many chunks per packet, all below index 20. */
const MAX_CHUNKS = 4
const MAX_CHUNK_INDEX = 20

/** A packet: its type, then its arguments. */
export type Packet = [string, ...Value[]]

export class ProtocolError extends Error {}

export interface Limits {
  /** The most bytes one piece may have on the wire. */
  maxPiece: number
  /**
   * The most bytes one packet may have once decompressed, its chunks
   * included: LZ4 makes up to 255 times what it is given.
   */
  maxInflated: number
}

/** Room for a 4K window's raw pixels (32 MiB), not enough to sink the tab. */
export const defaultLimits: Limits = { maxPiece: 64 << 20, maxInflated: 64 << 20 }

/** Reads packets from Xpra's byte stream, however it is cut up. */
export class PacketReader {
  private queue: Uint8Array[] = []
  private queued = 0
  private header: DataView | null = null
  private chunks = new Map<number, Uint8Array>()
  /** The bytes of the chunks held for the next packet. */
  private held = 0

  constructor(private readonly limits: Limits = defaultLimits) {}

  /** Takes the next bytes of the stream; returns the packets they complete. */
  push(bytes: Uint8Array): Packet[] {
    if (bytes.length) {
      this.queue.push(bytes)
      this.queued += bytes.length
    }
    const out: Packet[] = []
    for (;;) {
      if (!this.header) {
        if (this.queued < HEADER_SIZE) return out
        const h = this.take(HEADER_SIZE)
        this.header = new DataView(h.buffer, h.byteOffset, HEADER_SIZE)
        this.check(this.header)
      }
      const size = this.header.getUint32(4)
      if (this.queued < size) return out
      const h = this.header
      this.header = null
      const packet = this.piece(h, this.take(size))
      if (packet) out.push(packet)
    }
  }

  private check(h: DataView) {
    if (h.getUint8(0) !== MAGIC) throw new ProtocolError('not an Xpra packet')
    const flags = h.getUint8(1) & ~FLAG_FLUSH
    if (flags & FLAG_CIPHER) throw new ProtocolError('encrypted packets are not supported')
    if (flags !== FLAG_RENCODEPLUS) throw new ProtocolError(`unsupported packet encoding ${flags}`)
    const level = h.getUint8(2)
    if (level !== 0 && (level & 0xf0) !== COMPRESS_LZ4) throw new ProtocolError(`unsupported compression ${level}`)
    if (h.getUint32(4) > this.limits.maxPiece) throw new ProtocolError(`a piece of ${h.getUint32(4)} bytes`)
  }

  private piece(h: DataView, data: Uint8Array): Packet | null {
    if (h.getUint8(2) !== 0) data = lz4Decode(data, this.limits.maxInflated - this.held)
    if (this.held + data.length > this.limits.maxInflated) throw new ProtocolError('a packet too large')
    const index = h.getUint8(3)
    if (index > 0) {
      if (index >= MAX_CHUNK_INDEX || this.chunks.has(index) || this.chunks.size >= MAX_CHUNKS) {
        throw new ProtocolError(`unexpected chunk ${index}`)
      }
      this.chunks.set(index, data)
      this.held += data.length
      return null
    }
    const v = decode(data)
    if (!Array.isArray(v) || v.length === 0) throw new ProtocolError('a packet is not a list')
    let type = v[0]
    if (type instanceof Uint8Array) type = new TextDecoder().decode(type)
    if (typeof type !== 'string') throw new ProtocolError('a packet without a type')
    for (const [i, chunk] of this.chunks) {
      if (i >= v.length) throw new ProtocolError(`chunk ${i} outside its packet`)
      v[i] = chunk
    }
    this.chunks.clear()
    this.held = 0
    return [type, ...v.slice(1)]
  }

  /** Takes n queued bytes, copying only when they span pieces. */
  private take(n: number): Uint8Array {
    const first = this.queue[0]
    let out: Uint8Array
    if (first && first.length >= n) {
      out = first.subarray(0, n)
      if (first.length === n) this.queue.shift()
      else this.queue[0] = first.subarray(n)
    } else {
      out = new Uint8Array(n)
      let at = 0
      while (at < n) {
        const b = this.queue[0]
        const k = Math.min(b.length, n - at)
        out.set(b.subarray(0, k), at)
        at += k
        if (k === b.length) this.queue.shift()
        else this.queue[0] = b.subarray(k)
      }
    }
    this.queued -= n
    return out
  }
}

/** Encodes a packet, uncompressed: Tilde sends little. */
export function encodePacket(type: string, args: readonly Encodable[]): Uint8Array {
  const body = encode([type, ...args])
  const out = new Uint8Array(HEADER_SIZE + body.length)
  const h = new DataView(out.buffer)
  h.setUint8(0, MAGIC)
  h.setUint8(1, FLAG_RENCODEPLUS)
  h.setUint32(4, body.length)
  out.set(body, HEADER_SIZE)
  return out
}
