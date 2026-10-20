import { describe, expect, it } from 'vitest'
import { baseName, dirName, joinPath, uniqueName } from './fs'

describe('path helpers', () => {
  it.each([
    ['/', 'a', '/a'],
    ['/home/me', 'a b', '/home/me/a b'],
  ])('joinPath(%s, %s)', (dir, name, want) => {
    expect(joinPath(dir, name)).toBe(want)
  })

  it.each([
    ['/', '/'],
    ['/etc', 'etc'],
    ['/home/me/notes.txt', 'notes.txt'],
  ])('baseName(%s)', (path, want) => {
    expect(baseName(path)).toBe(want)
  })

  it.each([
    ['/', '/'],
    ['/etc', '/'],
    ['/home/me/notes.txt', '/home/me'],
  ])('dirName(%s)', (path, want) => {
    expect(dirName(path)).toBe(want)
  })
})

describe('uniqueName', () => {
  it('numbers from 2 like Nemo', () => {
    expect(uniqueName('Untitled Folder', new Set())).toBe('Untitled Folder')
    expect(uniqueName('Untitled Folder', new Set(['Untitled Folder']))).toBe('Untitled Folder 2')
    expect(uniqueName('Untitled Folder', new Set(['Untitled Folder', 'Untitled Folder 2']))).toBe('Untitled Folder 3')
  })
})
