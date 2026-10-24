import { describe, expect, it } from 'vitest'
import { badName, filterAccepts, mimeMatches, normalizePath, resolveTyped, splitTyped } from './chooser'

const file = (name: string, mime: string) => ({ name, mime, type: 'file' as const })

describe('filters', () => {
  it('matches MIME types exactly or by major type', () => {
    expect(mimeMatches('text/x-go', ['text/*'])).toBe(true)
    expect(mimeMatches('application/json', ['application/json'])).toBe(true)
    expect(mimeMatches('application/pdf', ['text/*', 'application/json'])).toBe(false)
  })

  it('lists folders always, files by type or name pattern', () => {
    const f = { name: 'Text', mimeTypes: ['text/*'], patterns: ['*.CONF', 'notes-??.md'] }
    expect(filterAccepts(f, { name: 'Pictures', mime: 'inode/directory', type: 'dir' })).toBe(true)
    expect(filterAccepts(f, file('a.txt', 'text/plain'))).toBe(true)
    expect(filterAccepts(f, file('app.conf', 'application/octet-stream'))).toBe(true)
    expect(filterAccepts(f, file('notes-01.md', 'application/octet-stream'))).toBe(true)
    expect(filterAccepts(f, file('notes-1.md', 'application/octet-stream'))).toBe(false)
    expect(filterAccepts(f, file('photo.png', 'image/png'))).toBe(false)
    // Regular expression characters in a pattern are literal.
    expect(filterAccepts({ name: 'x', patterns: ['a.b'] }, file('axb', ''))).toBe(false)
  })

  it('lists everything without a filter, or with an empty one', () => {
    expect(filterAccepts(undefined, file('photo.png', 'image/png'))).toBe(true)
    expect(filterAccepts({ name: 'All Files' }, file('photo.png', 'image/png'))).toBe(true)
  })
})

describe('typed locations', () => {
  it('resolves absolute, home and relative paths', () => {
    expect(resolveTyped('/etc//hosts', '/home/me', '/home/me')).toBe('/etc/hosts')
    expect(resolveTyped('~', '/tmp', '/home/me')).toBe('/home/me')
    expect(resolveTyped('~/notes/', '/tmp', '/home/me')).toBe('/home/me/notes')
    expect(resolveTyped('../x', '/home/me/a', '/home/me')).toBe('/home/me/x')
    expect(resolveTyped('/..', '/', '/home/me')).toBe('/')
    expect(normalizePath('/a/./b/../c')).toBe('/a/c')
  })

  it('splits a typed name into a folder and a file name', () => {
    expect(splitTyped('today.txt', '/home/me', '/home/me')).toEqual({ dir: '/home/me', name: 'today.txt' })
    expect(splitTyped(' notes/today.txt ', '/home/me', '/home/me')).toEqual({
      dir: '/home/me/notes',
      name: 'today.txt',
    })
    expect(splitTyped('notes/', '/home/me', '/home/me')).toEqual({ dir: '/home/me/notes', name: '' })
    expect(splitTyped('', '/home/me', '/home/me')).toEqual({ dir: '/home/me', name: '' })
  })

  it('names what a file cannot be called', () => {
    expect(badName('')).not.toBe('')
    expect(badName('..')).not.toBe('')
    expect(badName('x'.repeat(256))).not.toBe('')
    expect(badName('report.txt')).toBe('')
  })
})
