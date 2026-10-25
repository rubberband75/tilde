import { describe, expect, it } from 'vitest'
import type { Entry } from '@/api/types'
import { folderSiblings, neighbour } from '@/lib/folderSiblings'
import { formatTime, isMedia } from './media'

const file = (name: string, mime: string): Entry => ({
  name,
  path: `/music/${name}`,
  type: 'file',
  size: 1,
  mode: '',
  perm: 0o644,
  uid: 1000,
  gid: 1000,
  mtime: '',
  mime,
})

describe('media files', () => {
  it('plays sound and video, not playlists or other files', () => {
    expect(isMedia(file('a.mp3', 'audio/mpeg'))).toBe(true)
    expect(isMedia(file('b.webm', 'video/webm'))).toBe(true)
    expect(isMedia(file('c.ogg', 'audio/x-vorbis+ogg'))).toBe(true)
    expect(isMedia(file('list.m3u', 'audio/x-mpegurl'))).toBe(false)
    expect(isMedia(file('cover.jpg', 'image/jpeg'))).toBe(false)
  })

  it('makes a playlist of the folder that stops at its ends', () => {
    const list = folderSiblings(
      [file('10 End.mp3', 'audio/mpeg'), file('2 Middle.mp3', 'audio/mpeg'), file('cover.jpg', 'image/jpeg')],
      '/music/2 Middle.mp3',
      isMedia,
    )
    expect(list.map((e) => e.name)).toEqual(['2 Middle.mp3', '10 End.mp3'])
    expect(neighbour(list, '/music/2 Middle.mp3', 1, false)?.name).toBe('10 End.mp3')
    expect(neighbour(list, '/music/10 End.mp3', 1, false)).toBeUndefined()
    expect(neighbour(list, '/music/2 Middle.mp3', -1, false)).toBeUndefined()
  })
})

describe('formatTime', () => {
  it('reads as minutes, or hours past an hour', () => {
    expect(formatTime(0)).toBe('0:00')
    expect(formatTime(245.7)).toBe('4:05')
    expect(formatTime(3723)).toBe('1:02:03')
    expect(formatTime(Number.NaN)).toBe('0:00')
    expect(formatTime(Number.POSITIVE_INFINITY)).toBe('0:00')
  })
})
