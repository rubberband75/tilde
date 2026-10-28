import { describe, expect, it } from 'vitest'
import type { DraftInfo } from '@/api/types'
import { recoverable, shownDraftKeys } from './recovery'

const draft = (key: string, preview = '', updated = '2026-10-28T10:00:00Z'): DraftInfo => ({
  key,
  preview,
  updated,
  size: 10,
})

describe('recoverable drafts', () => {
  it('leaves out what editor windows show', () => {
    const shown = shownDraftKeys([
      { path: '/home/me/open.txt', draft: 'x' },
      { draft: 'abc' },
      { draft: 'saved-since', path: '' },
      {},
    ])
    expect([...shown]).toEqual(['file:/home/me/open.txt', 'new:abc', 'new:saved-since'])

    const list = recoverable(
      [
        draft('file:/home/me/open.txt'),
        draft('new:abc', 'shown'),
        draft('file:/home/me/notes/todo.md', 'buy milk'),
        draft('new:def', 'Shopping list'),
        draft('new:ghi'),
        draft('other:thing'),
      ],
      shown,
    )
    expect(list.map(({ name, where, args }) => ({ name, where, args }))).toEqual([
      { name: 'todo.md', where: '/home/me/notes', args: { path: '/home/me/notes/todo.md' } },
      { name: 'Shopping list', where: 'Never saved', args: { draft: 'def' } },
      { name: 'Untitled Document', where: 'Never saved', args: { draft: 'ghi' } },
    ])
  })
})
