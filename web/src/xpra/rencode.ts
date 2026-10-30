// rencodeplus, the encoding of Xpra's packets (D80): Xpra 6 has no other.
// Written from Xpra's xpra/net/rencodeplus/rencodeplus.pyx. Each value
// starts with a type byte; small integers, short strings, lists and dicts
// carry their value or length in it. Text and bytes differ on the wire
// ("5:hello" and "5/hello" for longer ones), and stay string and
// Uint8Array here.
//
// What Xpra sends is untrusted (host apps choose titles, clipboard
// contents and the rest), so decoding checks every length against the
// input, bounds nesting, and builds dicts without a prototype.

/** A decoded value. Integers beyond 2^53 are bigints. */
export type Value = number | bigint | string | Uint8Array | boolean | null | Value[] | Dict

/** A decoded dict. Its keys are strings, whatever they were on the wire. */
export interface Dict {
  [key: string]: Value
}

/**
 * What encode takes: values, and Maps for dicts with integer keys (Xpra's
 * monitors). Undefined members of objects are left out.
 */
export type Encodable =
  | number
  | bigint
  | string
  | Uint8Array
  | boolean
  | null
  | readonly Encodable[]
  | EncodableDict
  | ReadonlyMap<string | number, Encodable>

export interface EncodableDict {
  readonly [key: string]: Encodable | undefined
}

const CHR_LIST = 59
const CHR_DICT = 60
const CHR_INT = 61
const CHR_INT1 = 62
const CHR_INT2 = 63
const CHR_INT4 = 64
const CHR_INT8 = 65
const CHR_FLOAT32 = 66
const CHR_FLOAT64 = 44
const CHR_TRUE = 67
const CHR_FALSE = 68
const CHR_NONE = 69
const CHR_TERM = 127
const INT_POS_FIXED_COUNT = 44
const INT_NEG_FIXED_START = 70
const INT_NEG_FIXED_COUNT = 32
const DICT_FIXED_START = 102
const DICT_FIXED_COUNT = 25
const STR_FIXED_START = 128
const STR_FIXED_COUNT = 64
const LIST_FIXED_START = STR_FIXED_START + STR_FIXED_COUNT
const LIST_FIXED_COUNT = 64
/** Python's limit for an integer written out in decimal. */
const MAX_INT_LENGTH = 64
const MAX_DEPTH = 100
/**
 * The most values one decode makes: each costs far more in JavaScript than
 * its byte on the wire, and a packet of a million is not one Xpra sends.
 */
const MAX_VALUES = 1_000_000

const INT64_MIN = -(2n ** 63n)
const INT64_MAX = 2n ** 63n - 1n

const utf8 = new TextEncoder()
const fromUtf8 = new TextDecoder()

export class RencodeError extends Error {}

class Writer {
  buf = new Uint8Array(256)
  view = new DataView(this.buf.buffer)
  pos = 0

  room(n: number) {
    if (this.pos + n <= this.buf.length) return
    let size = this.buf.length * 2
    while (size < this.pos + n) size *= 2
    const buf = new Uint8Array(size)
    buf.set(this.buf.subarray(0, this.pos))
    this.buf = buf
    this.view = new DataView(buf.buffer)
  }

  byte(b: number) {
    this.room(1)
    this.buf[this.pos++] = b
  }

  bytes(b: Uint8Array) {
    this.room(b.length)
    this.buf.set(b, this.pos)
    this.pos += b.length
  }

  ascii(s: string) {
    this.room(s.length)
    for (let i = 0; i < s.length; i++) this.buf[this.pos++] = s.charCodeAt(i)
  }
}

/** Encodes a value as Xpra's Python encoder would. */
export function encode(value: Encodable): Uint8Array {
  const w = new Writer()
  write(w, value, 0)
  return w.buf.slice(0, w.pos)
}

function write(w: Writer, v: Encodable, depth: number) {
  if (depth > MAX_DEPTH) throw new RencodeError('nested too deeply')
  if (v === null) w.byte(CHR_NONE)
  else if (typeof v === 'boolean') w.byte(v ? CHR_TRUE : CHR_FALSE)
  else if (typeof v === 'number') writeNumber(w, v)
  else if (typeof v === 'bigint') writeBigint(w, v)
  else if (typeof v === 'string') {
    const b = utf8.encode(v)
    if (b.length < STR_FIXED_COUNT) w.byte(STR_FIXED_START + b.length)
    else w.ascii(`${b.length}:`)
    w.bytes(b)
  } else if (v instanceof Uint8Array) {
    w.ascii(`${v.length}/`)
    w.bytes(v)
  } else if (Array.isArray(v)) {
    const list = v as readonly Encodable[]
    const fixed = list.length < LIST_FIXED_COUNT
    w.byte(fixed ? LIST_FIXED_START + list.length : CHR_LIST)
    for (const item of list) write(w, item, depth + 1)
    if (!fixed) w.byte(CHR_TERM)
  } else {
    const entries: [string | number, Encodable][] =
      v instanceof Map
        ? [...(v as ReadonlyMap<string | number, Encodable>)]
        : Object.entries(v as EncodableDict).filter((e): e is [string, Encodable] => e[1] !== undefined)
    const fixed = entries.length < DICT_FIXED_COUNT
    w.byte(fixed ? DICT_FIXED_START + entries.length : CHR_DICT)
    for (const [k, item] of entries) {
      write(w, k, depth + 1)
      write(w, item, depth + 1)
    }
    if (!fixed) w.byte(CHR_TERM)
  }
}

function writeNumber(w: Writer, n: number) {
  if (!Number.isInteger(n)) {
    w.byte(CHR_FLOAT64)
    w.room(8)
    w.view.setFloat64(w.pos, n)
    w.pos += 8
  } else if (n >= 0 && n < INT_POS_FIXED_COUNT) w.byte(n)
  else if (n < 0 && n >= -INT_NEG_FIXED_COUNT) w.byte(INT_NEG_FIXED_START - 1 - n)
  else if (n >= -128 && n < 128) {
    w.byte(CHR_INT1)
    w.room(1)
    w.view.setInt8(w.pos++, n)
  } else if (n >= -32768 && n < 32768) {
    w.byte(CHR_INT2)
    w.room(2)
    w.view.setInt16(w.pos, n)
    w.pos += 2
  } else if (n >= -(2 ** 31) && n < 2 ** 31) {
    w.byte(CHR_INT4)
    w.room(4)
    w.view.setInt32(w.pos, n)
    w.pos += 4
  } else writeBigint(w, BigInt(n))
}

function writeBigint(w: Writer, n: bigint) {
  if (n >= -(2n ** 31n) && n < 2n ** 31n) writeNumber(w, Number(n))
  else if (n >= INT64_MIN && n <= INT64_MAX) {
    w.byte(CHR_INT8)
    w.room(8)
    w.view.setBigInt64(w.pos, n)
    w.pos += 8
  } else {
    const s = n.toString()
    if (s.length >= MAX_INT_LENGTH) throw new RencodeError('integer too large')
    w.byte(CHR_INT)
    w.ascii(s)
    w.byte(CHR_TERM)
  }
}

/**
 * Decodes one value that fills data. Byte strings in the result are views
 * of data, not copies: pixels are not copied again.
 */
export function decode(data: Uint8Array): Value {
  const r = new Reader(data)
  const v = r.value(0)
  if (r.pos !== data.length) throw new RencodeError(`${data.length - r.pos} bytes after the value`)
  return v
}

class Reader {
  pos = 0
  values = 0
  readonly view: DataView

  constructor(readonly data: Uint8Array) {
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
  }

  /** Checks that n more bytes are there, and returns where they start. */
  take(n: number): number {
    if (this.pos + n > this.data.length) throw new RencodeError('truncated')
    const at = this.pos
    this.pos += n
    return at
  }

  value(depth: number): Value {
    if (depth > MAX_DEPTH) throw new RencodeError('nested too deeply')
    if (++this.values > MAX_VALUES) throw new RencodeError('too many values')
    const t = this.data[this.take(1)]
    if (t < INT_POS_FIXED_COUNT) return t
    if (t >= INT_NEG_FIXED_START && t < INT_NEG_FIXED_START + INT_NEG_FIXED_COUNT) return INT_NEG_FIXED_START - 1 - t
    if (t >= STR_FIXED_START && t < STR_FIXED_START + STR_FIXED_COUNT) {
      const n = t - STR_FIXED_START
      const at = this.take(n)
      return fromUtf8.decode(this.data.subarray(at, at + n))
    }
    if (t >= LIST_FIXED_START) return this.list(depth, t - LIST_FIXED_START)
    if (t >= DICT_FIXED_START && t < DICT_FIXED_START + DICT_FIXED_COUNT) return this.dict(depth, t - DICT_FIXED_START)
    if (t >= 48 && t <= 57) return this.string(t)
    switch (t) {
      case CHR_INT1:
        return this.view.getInt8(this.take(1))
      case CHR_INT2:
        return this.view.getInt16(this.take(2))
      case CHR_INT4:
        return this.view.getInt32(this.take(4))
      case CHR_INT8:
        return narrow(this.view.getBigInt64(this.take(8)))
      case CHR_INT:
        return this.bigNumber()
      case CHR_FLOAT32:
        return this.view.getFloat32(this.take(4))
      case CHR_FLOAT64:
        return this.view.getFloat64(this.take(8))
      case CHR_TRUE:
        return true
      case CHR_FALSE:
        return false
      case CHR_NONE:
        return null
      case CHR_LIST:
        return this.list(depth, -1)
      case CHR_DICT:
        return this.dict(depth, -1)
    }
    throw new RencodeError(`unknown type ${t}`)
  }

  /** "5:hello" (text) or "5/hello" (bytes); the first digit is read. */
  string(first: number): string | Uint8Array {
    let n = first - 48
    for (;;) {
      const c = this.data[this.take(1)]
      if (c === 58 || c === 47) {
        const at = this.take(n)
        const b = this.data.subarray(at, at + n)
        return c === 47 ? b : fromUtf8.decode(b)
      }
      if (c < 48 || c > 57) throw new RencodeError('bad string length')
      n = n * 10 + c - 48
      if (n > this.data.length) throw new RencodeError('truncated')
    }
  }

  bigNumber(): number | bigint {
    const start = this.pos
    while (this.data[this.take(1)] !== CHR_TERM) {
      if (this.pos - start > MAX_INT_LENGTH) throw new RencodeError('integer too long')
    }
    const s = fromUtf8.decode(this.data.subarray(start, this.pos - 1))
    if (!/^-?\d+$/.test(s)) throw new RencodeError('bad integer')
    return narrow(BigInt(s))
  }

  /** A list of n items, or up to the terminator for n < 0. */
  list(depth: number, n: number): Value[] {
    const out: Value[] = []
    while (n < 0 ? !this.end() : out.length < n) out.push(this.value(depth + 1))
    return out
  }

  dict(depth: number, n: number): Dict {
    const out: Dict = Object.create(null) as Dict
    for (let i = 0; n < 0 ? !this.end() : i < n; i++) {
      const k = this.value(depth + 1)
      out[key(k)] = this.value(depth + 1)
    }
    return out
  }

  /** At the terminator of a list or dict: takes it. */
  end(): boolean {
    if (this.data[this.pos] !== CHR_TERM) {
      if (this.pos >= this.data.length) throw new RencodeError('truncated')
      return false
    }
    this.pos++
    return true
  }
}

function key(k: Value): string {
  if (typeof k === 'string') return k
  if (typeof k === 'number' || typeof k === 'bigint') return String(k)
  if (k instanceof Uint8Array) return fromUtf8.decode(k)
  throw new RencodeError('bad dict key')
}

function narrow(n: bigint): number | bigint {
  return n >= BigInt(Number.MIN_SAFE_INTEGER) && n <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(n) : n
}
