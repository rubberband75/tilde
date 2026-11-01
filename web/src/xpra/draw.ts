// Xpra's draw packets (D80): checked, since what Xpra sends is untrusted,
// and raw pixels made into what a canvas takes. No DOM, so it runs in the
// decode worker and in tests alike. From xpra-html5's Window.js and
// RgbHelpers.js, for the packet's layout.

import type { Packet } from './framing'
import { lz4Decode } from './lz4'
import type { Dict, Value } from './rencode'

/** The largest side of a host window Tilde draws. */
export const MAX_WINDOW_SIDE = 8192

/** The most regions one scroll moves: each is a copy on the page's thread. */
const MAX_SCROLLS = 1024
/** Padding rows may have beyond their pixels. */
const MAX_ROW_PADDING = 64

/** One region a scroll packet moves: where from, its size, and how far. */
export type ScrollRect = [sx: number, sy: number, sw: number, sh: number, dx: number, dy: number]

export interface Draw {
  wid: number
  x: number
  y: number
  w: number
  h: number
  coding: string
  data: Uint8Array
  /** What the acknowledgement names. */
  seq: number
  rowstride: number
  options: Dict
  scrolls: ScrollRect[]
}

/** The encodings Tilde asks for; image types the browser decodes. */
const imageTypes = new Map([
  ['png', 'image/png'],
  ['png/P', 'image/png'],
  ['png/L', 'image/png'],
  ['jpeg', 'image/jpeg'],
  ['webp', 'image/webp'],
])
const rawCodings = new Set(['rgb24', 'rgb32'])

export const encodings = [...imageTypes.keys(), 'rgb', ...rawCodings, 'scroll', 'void']

export function isInt(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v)
}

export function isDict(v: Value | undefined): v is Dict {
  return typeof v === 'object' && v !== null && !Array.isArray(v) && !(v instanceof Uint8Array)
}

/**
 * Reads a draw packet: ["draw", wid, x, y, w, h, coding, data, seq,
 * rowstride, options]. Null when it is not one: nothing can be painted,
 * nor acknowledged.
 */
export function parseDraw(p: Packet): Draw | null {
  const [, wid, x, y, w, h, coding, data, seq, rowstride, options] = p
  if (!isInt(wid) || !isInt(x) || !isInt(y) || !isInt(w) || !isInt(h) || !isInt(seq)) return null
  if (typeof coding !== 'string') return null
  if (w < 0 || h < 0 || w > MAX_WINDOW_SIDE || h > MAX_WINDOW_SIDE) return null
  const opts: Dict = isDict(options) ? options : (Object.create(null) as Dict)
  const scrolls = coding === 'scroll' ? scrollRects(opts.scroll ?? data) : []
  if (scrolls.length > MAX_SCROLLS) return null
  return {
    wid,
    x,
    y,
    w,
    h,
    coding,
    data: data instanceof Uint8Array ? data : new Uint8Array(0),
    seq,
    rowstride: isInt(rowstride) ? rowstride : 0,
    options: opts,
    // Newer servers put them in the options, older ones in the data.
    scrolls,
  }
}

function scrollRects(v: Value | undefined): ScrollRect[] {
  if (!Array.isArray(v)) return []
  return v.filter((r): r is ScrollRect => Array.isArray(r) && r.length >= 6 && r.slice(0, 6).every(isInt))
}

/**
 * Whether a draw's regions are all inside a window of this size. Xpra's own
 * never go outside; a draw that does could make the page allocate for far more
 * than the window holds.
 */
export function drawFits(d: Draw, width: number, height: number): boolean {
  const inside = (x: number, y: number, w: number, h: number) =>
    x >= 0 && y >= 0 && w >= 0 && h >= 0 && x + w <= width && y + h <= height
  if (d.coding === 'scroll') {
    return d.scrolls.every(([sx, sy, sw, sh, dx, dy]) => inside(sx, sy, sw, sh) && inside(sx + dx, sy + dy, sw, sh))
  }
  return d.coding === 'void' || inside(d.x, d.y, d.w, d.h)
}

/**
 * The size an encoded image says it has, read from its header: decoding it
 * to find out is what a small file with a huge size is made to exploit. Null
 * when it does not say.
 */
export function imageSize(data: Uint8Array, type: string): { w: number; h: number } | null {
  const v = new DataView(data.buffer, data.byteOffset, data.byteLength)
  const tag = (at: number, s: string) => [...s].every((c, i) => data[at + i] === c.charCodeAt(0))
  if (type === 'image/png') {
    if (data.length < 24 || !tag(1, 'PNG') || !tag(12, 'IHDR')) return null
    return { w: v.getUint32(16), h: v.getUint32(20) }
  }
  if (type === 'image/jpeg') {
    if (data[0] !== 0xff || data[1] !== 0xd8) return null
    // Segments, each with its length, up to the frame's header (SOF).
    for (let at = 2; at + 9 <= data.length; at += 2 + v.getUint16(at + 2)) {
      if (data[at] !== 0xff) return null
      const m = data[at + 1]
      if (m >= 0xc0 && m <= 0xcf && m !== 0xc4 && m !== 0xc8 && m !== 0xcc) {
        return { w: v.getUint16(at + 7), h: v.getUint16(at + 5) }
      }
    }
    return null
  }
  if (type === 'image/webp') {
    if (data.length < 30 || !tag(0, 'RIFF') || !tag(8, 'WEBP')) return null
    if (tag(12, 'VP8 ')) return { w: v.getUint16(26, true) & 0x3fff, h: v.getUint16(28, true) & 0x3fff }
    if (tag(12, 'VP8L')) {
      const bits = v.getUint32(21, true)
      return { w: (bits & 0x3fff) + 1, h: ((bits >> 14) & 0x3fff) + 1 }
    }
    if (tag(12, 'VP8X')) {
      const u24 = (at: number) => data[at] | (data[at + 1] << 8) | (data[at + 2] << 16)
      return { w: u24(24) + 1, h: u24(27) + 1 }
    }
  }
  return null
}

/** The MIME type of an encoded image, or undefined for anything else. */
export function imageType(coding: string): string | undefined {
  return imageTypes.get(coding)
}

export function isRaw(coding: string): boolean {
  return rawCodings.has(coding)
}

/**
 * Raw pixels as RGBA, row after row with no padding. Opaque: Tilde asks for
 * no transparency yet, and the fourth byte of RGBX means nothing.
 */
export function rgbaOf(d: Draw): Uint8ClampedArray<ArrayBuffer> {
  let src = d.data
  if (d.options.zlib) throw new Error('zlib pixels are not supported')
  const bpp = d.coding === 'rgb24' ? 3 : 4
  const stride = d.rowstride || d.w * bpp
  if (stride < d.w * bpp) throw new Error('rows shorter than the width')
  if (stride > d.w * bpp + MAX_ROW_PADDING) throw new Error('rows longer than they should be')
  if (isInt(d.options.lz4) && d.options.lz4 > 0) src = lz4Decode(src, stride * d.h)
  if (d.h > 0 && src.length < stride * (d.h - 1) + d.w * bpp) throw new Error('too few pixels')
  const out = new Uint8ClampedArray(d.w * d.h * 4)
  let o = 0
  for (let row = 0; row < d.h; row++) {
    let s = row * stride
    for (let col = 0; col < d.w; col++) {
      out[o++] = src[s]
      out[o++] = src[s + 1]
      out[o++] = src[s + 2]
      out[o++] = 255
      s += bpp
    }
  }
  return out
}
