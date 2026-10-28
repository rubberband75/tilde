import { describe, expect, it } from 'vitest'
import { appForMime, isTextMime } from './registry'

describe('isTextMime', () => {
  it('takes what the text editor opens, even where another app is the default', () => {
    expect(['text/plain', 'text/markdown', 'application/json', 'application/x-shellscript'].every(isTextMime)).toBe(
      true,
    )
    expect(['image/png', 'application/pdf', 'inode/directory', 'video/mp4'].some(isTextMime)).toBe(false)
    expect(appForMime('text/plain')?.id).toBe('text-editor')
  })
})
