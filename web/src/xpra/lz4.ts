// LZ4 for Xpra's packets (D80): Xpra compresses a packet, or a chunk of
// one, as one LZ4 block after its size (4 bytes, little-endian), as its
// xpra/net/lz4 module writes it. Written from the LZ4 block format
// (github.com/lz4/lz4, doc/lz4_Block_format.md). Every length and offset
// is checked: what Xpra sends is untrusted.

export class LZ4Error extends Error {}

/** Decompresses Xpra's LZ4 payload, refusing more than max bytes out. */
export function lz4Decode(data: Uint8Array, max: number): Uint8Array {
  if (data.length < 4) throw new LZ4Error('truncated')
  const size = (data[0] | (data[1] << 8) | (data[2] << 16) | (data[3] << 24)) >>> 0
  if (size === 0 || size > max) throw new LZ4Error(`bad size ${size}`)
  const out = new Uint8Array(size)
  if (decompressBlock(data, 4, out) !== size) throw new LZ4Error('short block')
  return out
}

/** Decompresses the block at src[start:] into dst; returns the bytes written. */
function decompressBlock(src: Uint8Array, start: number, dst: Uint8Array): number {
  let s = start
  let d = 0
  const length = (n: number) => {
    let b: number
    do {
      if (s >= src.length) throw new LZ4Error('truncated')
      b = src[s++]
      n += b
    } while (b === 255)
    return n
  }
  while (s < src.length) {
    const token = src[s++]
    let lit = token >> 4
    if (lit === 15) lit = length(lit)
    if (s + lit > src.length || d + lit > dst.length) throw new LZ4Error('literals overrun')
    dst.set(src.subarray(s, s + lit), d)
    s += lit
    d += lit
    // The last sequence has literals only.
    if (s === src.length) break
    if (s + 2 > src.length) throw new LZ4Error('truncated')
    const offset = src[s] | (src[s + 1] << 8)
    s += 2
    if (offset === 0 || offset > d) throw new LZ4Error('bad offset')
    let len = token & 15
    if (len === 15) len = length(len)
    len += 4
    if (d + len > dst.length) throw new LZ4Error('match overrun')
    if (offset >= len) {
      dst.copyWithin(d, d - offset, d - offset + len)
      d += len
    } else {
      // Overlapping: a run repeats as it is written.
      for (let m = d - offset, end = d + len; d < end;) dst[d++] = dst[m++]
    }
  }
  return d
}
