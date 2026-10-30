import { describe, expect, it } from 'vitest'
import { lz4Decode, LZ4Error } from './lz4'

const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0))
const text = (s: string) => new TextEncoder().encode(s)

/** The input of the long vector: repeats, every byte value, a long run, noise. */
function sample(): Uint8Array {
  return new Uint8Array([
    ...text('tilde '.repeat(1000)),
    ...Array.from({ length: 768 }, (_, i) => i % 256),
    ...text('x'.repeat(300)),
    ...Array.from({ length: 600 }, (_, i) => (i * 7919) % 251),
  ])
}

// Made by Python's lz4.block.compress(data, store_size=True), the format
// of Xpra's own lz4 module: the size, little-endian, then one block.
const long =
  '9B0AAG90aWxkZSAGAP//////////////////////////////bv/xAAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn+AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+/wAB/+4feAEA/xn/7ACKGaMyvEvVZO59DJYlrz7IV+Fw+okYojG7StRj7XwLlSSuPcdW4G/5iBehMLpJ02LsewqUI608xlXfbviHFqAvuUjSYet6CZMirDvFVN5t94YVny64R9Fg6nkIkiGrOsRT3Wz2hRSeLbdG0F/peAeRIKo5w1Lca/WEE50stkXPXuh3BpAfqTjCUdtq9IMSnCu1RM5d53YFjx6oN8FQ2mnzghGbKrRDzVzmdQSOHac2wE/ZaPKBEJops0LMW+V0A40cpjW/Tthn8YAPmSiyQcta5HMCjBulNL5N12bwfw6YJ7FAylnjcgGLGqQzvUzWZe9+DZcmsD/JWOJx+wD/RlAhqzrEUw=='
const short = 'DwAAADNhYmMDAFBiY2FiYw=='

describe('lz4Decode', () => {
  it('decompresses what Xpra compresses', () => {
    expect(lz4Decode(b64(long), 1 << 20)).toEqual(sample())
    expect(new TextDecoder().decode(lz4Decode(b64(short), 100))).toBe('abcabcabcabcabc')
  })

  it('refuses more than it may make', () => {
    expect(() => lz4Decode(b64(long), 7000)).toThrow(LZ4Error)
  })

  it.each([
    ['no size', [1, 0]],
    ['a size of zero', [0, 0, 0, 0, 0x10, 0x61]],
    ['literals past the end', [5, 0, 0, 0, 0x50, 0x61]],
    ['an offset before the start', [8, 0, 0, 0, 0x10, 0x61, 2, 0, 0x10, 0x61]],
    ['an offset of zero', [8, 0, 0, 0, 0x10, 0x61, 0, 0, 0x10, 0x61]],
    ['a match past the size', [5, 0, 0, 0, 0x1f, 0x61, 1, 0, 0x10, 0x61]],
    ['a short block', [9, 0, 0, 0, 0x30, 0x61, 0x62, 0x63]],
  ])('refuses %s', (_, data) => {
    expect(() => lz4Decode(new Uint8Array(data), 100)).toThrow(LZ4Error)
  })
})
