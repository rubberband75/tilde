import { describe, expect, it } from 'vitest'
import { isFolderDrag, isNoopDrop, isPathDrag, isUploadDrag, shellQuote } from './dnd'

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

describe('what a drag carries', () => {
  // jsdom has no DataTransfer: only the types are read while dragging.
  const drag = (...types: string[]) => ({ dataTransfer: { types } }) as unknown as DragEvent

  it('lets folders take paths and uploads, not an editor tab or text', () => {
    const paths = drag('application/x-tilde-paths', 'text/plain')
    const upload = drag('Files')
    const tab = drag('application/x-tilde-editor-tab')
    const text = drag('text/plain')
    expect([paths, upload, tab, text].map(isFolderDrag)).toEqual([true, true, false, false])
    expect([paths, upload].map(isPathDrag)).toEqual([true, false])
    expect([paths, upload].map(isUploadDrag)).toEqual([false, true])
  })
})
