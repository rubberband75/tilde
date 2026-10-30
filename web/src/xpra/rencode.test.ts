import { describe, expect, it } from 'vitest'
import { decode, encode, RencodeError, type Encodable } from './rencode'

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
const bytes = (h: string) => new Uint8Array((h.replace(/\s/g, '').match(/../g) ?? []).map((x) => parseInt(x, 16)))
const ascii = (s: string) => hex(Uint8Array.from(s, (c) => c.charCodeAt(0)))

// What Xpra's rencodeplus.dumps makes of each, worked out from
// rencodeplus.pyx: type codes, big-endian integers, "len:" text and
// "len/" bytes.
const vectors: [string, Encodable, string][] = [
  ['zero', 0, '00'],
  ['the last fixed positive', 43, '2b'],
  ['a byte', 44, '3e2c'],
  ['minus one', -1, '46'],
  ['the last fixed negative', -32, '65'],
  ['a negative byte', -33, '3edf'],
  ['a short', 200, '3f00c8'],
  ['a negative short', -200, '3fff38'],
  ['an int', 70000, '4000011170'],
  ['a long', 2 ** 31, '410000000080000000'],
  ['a big number', 2n ** 64n, '3d' + ascii('18446744073709551616') + '7f'],
  ['a float', 1.5, '2c3ff8000000000000'],
  ['true', true, '43'],
  ['false', false, '44'],
  ['None', null, '45'],
  ['empty text', '', '80'],
  ['text', 'hello', '85' + ascii('hello')],
  ['UTF-8', 'é', '82c3a9'],
  ['long text', 'x'.repeat(64), ascii('64:') + ascii('x'.repeat(64))],
  ['bytes', new Uint8Array([97, 98]), ascii('2/ab')],
  ['a list', [1, 2], 'c20102'],
  ['an empty list', [], 'c0'],
  ['a long list', new Array<number>(64).fill(0), '3b' + '00'.repeat(64) + '7f'],
  ['a dict', { a: 1 }, '678161' + '01'],
  ['integer keys', new Map([[0, {}]]), '670066'],
  [
    'a hello',
    ['hello', { version: '6.5.5' }],
    'c285' + ascii('hello') + '6787' + ascii('version') + '85' + ascii('6.5.5'),
  ],
]

describe('rencodeplus', () => {
  it.each(vectors)('encodes %s as Xpra does', (_, value, wire) => {
    expect(hex(encode(value))).toBe(wire)
  })

  it.each(vectors)('decodes %s', (_, value, wire) => {
    const want = value instanceof Map ? { 0: {} } : value
    expect(decode(bytes(wire))).toEqual(want)
  })

  it('leaves undefined members out', () => {
    expect(hex(encode({ a: 1, b: undefined }))).toBe('67816101')
  })

  it('keeps bytes apart from text, and shares them with the input', () => {
    const wire = bytes(ascii('3/abc'))
    const v = decode(wire) as Uint8Array
    expect(v).toBeInstanceOf(Uint8Array)
    expect(v.buffer).toBe(wire.buffer)
    expect(decode(bytes(ascii('3:abc')))).toBe('abc')
  })

  it('reads what Python sends but Tilde does not', () => {
    expect(decode(bytes('423fc00000'))).toBe(1.5) // float32
    expect(decode(bytes('41 00000000 00000005'))).toBe(5) // a long that fits
    expect(decode(bytes('41 7fffffff ffffffff'))).toBe(2n ** 63n - 1n)
    expect(decode(bytes('3c' + ascii('1:a') + '01' + '7f'))).toEqual({ a: 1 }) // a long dict, ended
    expect(decode(bytes('67' + ascii('1/k') + '01'))).toEqual({ k: 1 }) // a bytes key
  })

  it('keeps __proto__ a key like any other', () => {
    const d = decode(bytes('67' + '89' + ascii('__proto__') + '67' + '81' + ascii('x') + '01')) as Record<
      string,
      unknown
    >
    expect(Object.keys(d)).toEqual(['__proto__'])
    expect(({} as Record<string, unknown>).x).toBeUndefined()
  })

  it.each([
    ['truncated text', ascii('5:abc')],
    ['a truncated short', '3f00'],
    ['an unended list', '3b0102'],
    ['a list short of items', 'c301'],
    ['an unknown type', '2e'],
    ['bytes after the value', '0001'],
    ['a bad length', ascii('1x:a')],
    ['a huge length', ascii('99999999999:')],
    ['a bad big number', '3d' + ascii('12a') + '7f'],
    ['a list key', '67c001'],
    ['deep nesting', 'c1'.repeat(200) + '00'],
  ])('refuses %s', (_, wire) => {
    expect(() => decode(bytes(wire))).toThrow(RencodeError)
  })

  it('refuses more values than a packet has', () => {
    const many = new Uint8Array(1_000_002)
    many[0] = 0x3b // a list of a million and one zeros, unended
    expect(() => decode(many)).toThrow(/too many values/)
  })
})
