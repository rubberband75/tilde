import { describe, expect, it } from 'vitest'
import { encodePacket, PacketReader, ProtocolError } from './framing'
import { encode } from './rencode'

const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0))
const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
const text = (s: string) => new TextEncoder().encode(s)

function piece(body: Uint8Array, { flags = 0x10, level = 0, index = 0 } = {}): Uint8Array {
  const out = new Uint8Array(8 + body.length)
  const h = new DataView(out.buffer)
  h.setUint8(0, 0x50)
  h.setUint8(1, flags)
  h.setUint8(2, level)
  h.setUint8(3, index)
  h.setUint32(4, body.length)
  out.set(body, 8)
  return out
}

function concat(...parts: Uint8Array[]): Uint8Array {
  return new Uint8Array(parts.flatMap((p) => [...p]))
}

describe('encodePacket', () => {
  it('writes the header, then the packet in rencodeplus', () => {
    expect(hex(encodePacket('ping', [5]))).toBe('5010000000000007c284' + '70696e67' + '05')
  })
})

describe('PacketReader', () => {
  it('reads packets however the stream is cut', () => {
    const stream = concat(encodePacket('hello', [{ version: '6.5.5' }]), encodePacket('ping', [1234, 5678]))
    for (const step of [1, 3, 8, 9, stream.length]) {
      const r = new PacketReader()
      const got = []
      for (let at = 0; at < stream.length; at += step) got.push(...r.push(stream.subarray(at, at + step)))
      expect(got).toEqual([
        ['hello', { version: '6.5.5' }],
        ['ping', 1234, 5678],
      ])
    }
  })

  it('puts a compressed chunk back in its place', () => {
    // A draw's pixels sent ahead as chunk 7, its place in the packet,
    // compressed with LZ4 (level 1); the packet itself asks for a flush.
    const pixels = b64('DwAAADNhYmMDAFBiY2FiYw==')
    const stream = concat(
      piece(pixels, { level: 0x11, index: 7 }),
      piece(encode(['draw', 1, 0, 0, 5, 3, 'rgb24', '', 42]), { flags: 0x18 }),
    )
    const [p] = new PacketReader().push(stream)
    expect(p?.[0]).toBe('draw')
    expect(new TextDecoder().decode(p?.[7] as Uint8Array)).toBe('abcabcabcabcabc')
    expect(p?.[8]).toBe(42)
  })

  it('reads a whole compressed packet', () => {
    // ["abc..."] would not compress; LZ4 of encode(["ping", 1]) is literals only.
    const body = encode(['ping', 1])
    const lz4 = concat(new Uint8Array([body.length, 0, 0, 0, body.length << 4]), body)
    expect(new PacketReader().push(piece(lz4, { level: 0x13 }))).toEqual([['ping', 1]])
  })

  it.each([
    ['another protocol', text('GET / HTTP/1.1\r\n')],
    ['encryption', piece(encode(['ping']), { flags: 0x12 })],
    ['another encoding', piece(encode(['ping']), { flags: 0x01 })],
    ['Brotli', piece(encode(['ping']), { level: 0x45 })],
    ['a packet that is not a list', piece(encode({ a: 1 }))],
    ['a packet without a type', piece(encode([1, 2]))],
    ['a chunk outside its packet', concat(piece(new Uint8Array([1]), { index: 3 }), piece(encode(['ping'])))],
    ['a chunk twice', concat(piece(new Uint8Array([1]), { index: 1 }), piece(new Uint8Array([1]), { index: 1 }))],
    ['a chunk index too high', piece(new Uint8Array([1]), { index: 20 })],
  ])('refuses %s', (_, stream) => {
    expect(() => new PacketReader().push(stream)).toThrow(Error)
  })

  it("counts a packet's chunks against its limit", () => {
    const r = new PacketReader({ maxPiece: 100, maxInflated: 100 })
    const stream = concat(piece(new Uint8Array(60), { index: 1 }), piece(new Uint8Array(60), { index: 2 }))
    expect(() => r.push(stream)).toThrow(/too large/)
  })

  it('refuses a piece over the limit before it comes', () => {
    const r = new PacketReader({ maxPiece: 100, maxInflated: 100 })
    const header = piece(new Uint8Array(101)).subarray(0, 8)
    expect(() => r.push(header)).toThrow(ProtocolError)
  })
})
