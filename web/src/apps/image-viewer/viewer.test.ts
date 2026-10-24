import { describe, expect, it } from 'vitest'
import type { Entry } from '@/api/types'
import { fitZoom, folderImages, isViewable, neighbour, stepZoom } from './viewer'

function entry(name: string, mime: string, extra: Partial<Entry> = {}): Entry {
  return {
    name,
    path: `/pics/${name}`,
    type: 'file',
    size: 1,
    mode: '',
    perm: 0o644,
    uid: 1000,
    gid: 1000,
    mtime: '',
    mime,
    ...extra,
  }
}

describe('folder images', () => {
  const entries = [
    entry('img10.png', 'image/png'),
    entry('img2.jpg', 'image/jpeg'),
    entry('notes.txt', 'text/plain'),
    entry('scan.tiff', 'image/tiff'),
    entry('.secret.png', 'image/png', { hidden: true }),
    entry('Album', 'inode/directory', { type: 'dir' }),
    entry('logo.svg', 'image/svg+xml'),
  ]

  it('lists what the browser can draw, in name order', () => {
    expect(folderImages(entries, '/pics/img2.jpg').map((e) => e.name)).toEqual(['img2.jpg', 'img10.png', 'logo.svg'])
    expect(isViewable(entry('scan.tiff', 'image/tiff'))).toBe(false)
  })

  it('counts hidden images only when a hidden one is shown', () => {
    expect(folderImages(entries, '/pics/.secret.png').map((e) => e.name)).toContain('.secret.png')
  })

  it('wraps around at either end', () => {
    const list = folderImages(entries, '/pics/img2.jpg')
    expect(neighbour(list, '/pics/logo.svg', 1)?.name).toBe('img2.jpg')
    expect(neighbour(list, '/pics/img2.jpg', -1)?.name).toBe('logo.svg')
    expect(neighbour(list, '/pics/gone.png', 1)?.name).toBe('img2.jpg')
    expect(neighbour([], '/pics/x.png', 1)).toBeUndefined()
  })
})

describe('zoom', () => {
  it('steps in and out, also from between steps', () => {
    expect(stepZoom(1, 1)).toBe(1.33)
    expect(stepZoom(1, -1)).toBe(0.67)
    expect(stepZoom(0.8, 1)).toBe(1)
    expect(stepZoom(0.8, -1)).toBe(0.67)
    expect(stepZoom(20, 1)).toBe(20)
    expect(stepZoom(0.05, -1)).toBe(0.05)
  })

  it('fits the image without enlarging it, turned or not', () => {
    expect(fitZoom({ w: 4000, h: 2000 }, { w: 800, h: 600 }, 0)).toBe(0.2)
    expect(fitZoom({ w: 4000, h: 2000 }, { w: 800, h: 600 }, 1)).toBe(0.15)
    expect(fitZoom({ w: 100, h: 50 }, { w: 800, h: 600 }, 0)).toBe(1)
    expect(fitZoom({ w: 0, h: 0 }, { w: 800, h: 600 }, 0)).toBe(1)
  })
})
