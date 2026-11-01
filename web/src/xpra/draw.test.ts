import { describe, expect, it } from 'vitest'
import { drawFits, imageSize, imageType, isRaw, MAX_WINDOW_SIDE, parseDraw, rgbaOf, type Draw } from './draw'
import type { Packet } from './framing'
import type { Value } from './rencode'

const bytes = (...b: number[]) => new Uint8Array(b)

function packet(over: Partial<Record<'w' | 'h' | 'coding' | 'data' | 'stride' | 'options', Value>> = {}): Packet {
  const { w = 2, h = 1, coding = 'rgb24', data = bytes(), stride = 0, options = {} } = over
  return ['draw', 7, 10, 20, w, h, coding, data, 99, stride, options]
}

function raw(over: Parameters<typeof packet>[0]): Draw {
  return parseDraw(packet(over))!
}

describe('parseDraw', () => {
  it('reads the packet', () => {
    expect(parseDraw(packet({ data: bytes(1, 2) }))).toMatchObject({
      wid: 7,
      x: 10,
      y: 20,
      w: 2,
      h: 1,
      coding: 'rgb24',
      seq: 99,
      data: bytes(1, 2),
    })
  })

  it('takes scrolls from the options, or from the data', () => {
    const r = [0, 10, 100, 50, 0, -10]
    expect(parseDraw(packet({ coding: 'scroll', options: { scroll: [r] } }))?.scrolls).toEqual([r])
    expect(parseDraw(packet({ coding: 'scroll', data: [r] }))?.scrolls).toEqual([r])
    expect(parseDraw(packet({ coding: 'scroll', data: [[1, 2], 'x', r] }))?.scrolls).toEqual([r])
  })

  it.each<[string, Packet]>([
    ['no numbers', ['draw', 'a', 0, 0, 1, 1, 'png', bytes(), 1, 0, {}]],
    ['no sequence', ['draw', 1, 0, 0, 1, 1, 'png', bytes(), 'x', 0, {}]],
    ['a negative size', packet({ w: -1 })],
    ['too wide', packet({ w: MAX_WINDOW_SIDE + 1 })],
    ['no encoding', packet({ coding: 5 })],
    ['too short', ['draw', 1]],
    ['too many scrolls', packet({ coding: 'scroll', options: { scroll: new Array(1025).fill([0, 0, 1, 1, 0, 0]) } })],
  ])('refuses %s', (_, p) => {
    expect(parseDraw(p)).toBeNull()
  })
})

describe('rgbaOf', () => {
  it('fills in alpha for rgb24', () => {
    const d = raw({ data: bytes(1, 2, 3, 4, 5, 6) })
    expect([...rgbaOf(d)]).toEqual([1, 2, 3, 255, 4, 5, 6, 255])
  })

  it('makes rgb32 opaque: the fourth byte of RGBX means nothing', () => {
    const d = raw({ coding: 'rgb32', data: bytes(1, 2, 3, 0, 4, 5, 6, 9) })
    expect([...rgbaOf(d)]).toEqual([1, 2, 3, 255, 4, 5, 6, 255])
  })

  it('leaves out the padding of each row', () => {
    const d = raw({ w: 1, h: 2, stride: 5, data: bytes(1, 2, 3, 0, 0, 4, 5, 6) })
    expect([...rgbaOf(d)]).toEqual([1, 2, 3, 255, 4, 5, 6, 255])
  })

  it('decompresses what the options say is LZ4', () => {
    // 6 bytes: their size, then a block of literals only.
    const lz4 = bytes(6, 0, 0, 0, 0x60, 1, 2, 3, 4, 5, 6)
    const d = raw({ data: lz4, options: { lz4: 1 } })
    expect([...rgbaOf(d)]).toEqual([1, 2, 3, 255, 4, 5, 6, 255])
  })

  it.each<[string, Parameters<typeof packet>[0]]>([
    ['too few pixels', { data: bytes(1, 2, 3) }],
    ['rows far longer than the width', { w: 1, stride: 100, data: new Uint8Array(100) }],
    ['rows shorter than the width', { stride: 1, data: bytes(1, 2, 3, 4, 5, 6) }],
    ['zlib', { data: bytes(1, 2, 3, 4, 5, 6), options: { zlib: 1 } }],
    ['LZ4 that makes too much', { data: bytes(255, 255, 255, 0, 0x10, 1), options: { lz4: 1 } }],
  ])('refuses %s', (_, over) => {
    expect(() => rgbaOf(raw(over))).toThrow()
  })
})

describe('encodings', () => {
  it('knows images from raw pixels, and nothing else', () => {
    expect(imageType('png/P')).toBe('image/png')
    expect(imageType('webp')).toBe('image/webp')
    expect(imageType('h264')).toBeUndefined()
    expect(imageType('constructor')).toBeUndefined()
    expect(isRaw('rgb32')).toBe(true)
    expect(isRaw('png')).toBe(false)
  })
})

describe('drawFits', () => {
  const d = (over: Parameters<typeof packet>[0]) => parseDraw(packet(over))!
  it('takes draws inside the window, and scrolls that stay inside', () => {
    expect(drawFits(d({}), 12, 21)).toBe(true) // 2x1 at 10,20: touching both edges
    expect(drawFits(d({ coding: 'scroll', options: { scroll: [[0, 10, 5, 5, 0, -10]] } }), 5, 15)).toBe(true)
    expect(drawFits(d({ coding: 'void', w: 9000 > MAX_WINDOW_SIDE ? 0 : 1 }), 1, 1)).toBe(true)
  })
  it('refuses what goes outside', () => {
    expect(drawFits(d({}), 11, 21)).toBe(false)
    expect(drawFits(d({}), 12, 20)).toBe(false)
    expect(drawFits(d({ coding: 'scroll', options: { scroll: [[0, 0, 5, 5, 0, -1]] } }), 9, 9)).toBe(false)
    expect(drawFits(d({ coding: 'scroll', options: { scroll: [[0, 0, -5, 5, 0, 0]] } }), 9, 9)).toBe(false)
  })
})

describe('imageSize', () => {
  const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0))
  it('reads a PNG', () => {
    const png = b64('iVBORw0KGgoAAAANSUhEUgAAAAMAAAAFCAYAAAB4ka1VAAAADklEQVR4nGNgQAAmEAEAAB0ABDZyZ4wAAAAASUVORK5CYII=')
    expect(imageSize(png, 'image/png')).toEqual({ w: 3, h: 5 })
  })
  it('finds a JPEG frame after its other segments', () => {
    const j = bytes(
      0xff,
      0xd8,
      0xff,
      0xe0,
      0,
      4,
      1,
      2,
      0xff,
      0xc4,
      0,
      2,
      0xff,
      0xc0,
      0,
      11,
      8,
      1,
      0x40,
      2,
      0x80,
      3,
      0,
      0,
      0,
    )
    expect(imageSize(j, 'image/jpeg')).toEqual({ w: 0x280, h: 0x140 })
  })
  it('reads the three kinds of WebP', () => {
    const riff = (kind: string, body: number[]) => {
      const b = new Uint8Array(40)
      b.set(new TextEncoder().encode(`RIFF\0\0\0\0WEBP${kind}`))
      b.set(body, 20)
      return b
    }
    // VP8: a frame tag, a start code, then 14-bit sizes, little-endian.
    expect(imageSize(riff('VP8 ', [0, 0, 0, 0, 0, 0, 0x10, 0, 0x20, 0]), 'image/webp')).toEqual({ w: 16, h: 32 })
    // VP8L: a signature, then 14 bits each of size minus one.
    expect(imageSize(riff('VP8L', [0x2f, 15, 0xc0, 7, 0]), 'image/webp')).toEqual({ w: 16, h: 32 })
    // VP8X: 24 bits each of size minus one.
    expect(imageSize(riff('VP8X', [0, 0, 0, 0, 15, 0, 0, 31, 0, 0]), 'image/webp')).toEqual({ w: 16, h: 32 })
  })
  it.each<[string, Uint8Array, string]>([
    ['nothing', bytes(), 'image/png'],
    ['a short PNG', bytes(0x89, 0x50, 0x4e, 0x47), 'image/png'],
    ['a JPEG with a bad segment', bytes(0xff, 0xd8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0), 'image/jpeg'],
    ['a JPEG with no frame', bytes(0xff, 0xd8, 0xff, 0xe0, 0, 2), 'image/jpeg'],
    ['a WebP of no kind', new Uint8Array(40), 'image/webp'],
    ['another type', bytes(1, 2, 3), 'image/gif'],
  ])('says nothing of %s', (_, data, type) => {
    expect(imageSize(data, type)).toBeNull()
  })
})
