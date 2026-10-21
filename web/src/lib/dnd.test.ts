import { describe, expect, it } from 'vitest'
import { isNoopDrop, shellQuote } from './dnd'

describe('isNoopDrop', () => {
  it('ignores drops into the same folder or onto a dragged item', () => {
    expect(isNoopDrop(['/a/x', '/a/y'], '/a')).toBe(true)
    expect(isNoopDrop(['/a/x'], '/a/x')).toBe(true)
    expect(isNoopDrop(['/x'], '/')).toBe(true)
    expect(isNoopDrop(['/a/x', '/b/y'], '/a')).toBe(false)
    expect(isNoopDrop(['/a/x'], '/b')).toBe(false)
  })
})

describe('shellQuote', () => {
  it('quotes only what needs it', () => {
    expect(shellQuote(['/home/me/notes.txt'])).toBe('/home/me/notes.txt')
    expect(shellQuote(['/home/me/My Files', "/tmp/it's"])).toBe(`'/home/me/My Files' '/tmp/it'\\''s'`)
  })
})
