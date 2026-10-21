import { describe, expect, it } from 'vitest'
import type { Entry } from '@/api/types'
import { sortItems, type Item } from './types'

function item(name: string, over: Partial<Item> = {}, dir = false): Item {
  return {
    key: name,
    name,
    icon: [],
    type: dir ? 'Folder' : 'Plain text',
    mtimeMs: 0,
    entry: { type: dir ? 'dir' : 'file' } as Entry,
    ...over,
  }
}

const names = (list: Item[]) => list.map((i) => i.name)

describe('sortItems', () => {
  const list = [
    item('file10.txt', { size: 1, mtimeMs: 3 }),
    item('File2.txt', { size: 30, mtimeMs: 1 }),
    item('zeta', {}, true),
    item('alpha', {}, true),
    item('b.png', { size: 20, mtimeMs: 2, type: 'PNG image' }),
  ]

  it('puts folders first and sorts names naturally, ignoring case', () => {
    expect(names(sortItems(list, 'name', false))).toEqual(['alpha', 'zeta', 'b.png', 'File2.txt', 'file10.txt'])
  })

  it('keeps folders first when reversed', () => {
    expect(names(sortItems(list, 'name', true))).toEqual(['zeta', 'alpha', 'file10.txt', 'File2.txt', 'b.png'])
  })

  it('sorts by size, type and date', () => {
    expect(names(sortItems(list, 'size', false)).slice(2)).toEqual(['file10.txt', 'b.png', 'File2.txt'])
    expect(names(sortItems(list, 'type', false)).slice(2)).toEqual(['File2.txt', 'file10.txt', 'b.png'])
    expect(names(sortItems(list, 'mtime', true)).slice(2)).toEqual(['file10.txt', 'b.png', 'File2.txt'])
  })
})
