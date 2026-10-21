import { describe, expect, it } from 'vitest'
import { accessOf, isExecutable, withAccess, withExecutable } from './perms'

describe('file permissions', () => {
  it('reads the choices', () => {
    expect(accessOf(0o640, 'owner', false)).toBe('rw')
    expect(accessOf(0o640, 'group', false)).toBe('r')
    expect(accessOf(0o640, 'others', false)).toBe('none')
    expect(accessOf(0o755, 'group', false)).toBe('r') // x is the separate switch
    expect(accessOf(0o620, 'group', false)).toBe('custom') // write-only
  })

  it('changes one class and keeps execute for readers', () => {
    expect(withAccess(0o644, 'others', false, 'none')).toBe(0o640)
    expect(withAccess(0o755, 'group', false, 'rw')).toBe(0o775)
    expect(withAccess(0o755, 'others', false, 'none')).toBe(0o750)
  })

  it('toggles execute for everyone who can read', () => {
    expect(withExecutable(0o640, true)).toBe(0o750)
    expect(withExecutable(0o755, false)).toBe(0o644)
    expect(isExecutable(0o750)).toBe(true)
  })
})

describe('folder permissions', () => {
  it('maps the folder choices', () => {
    expect(accessOf(0o750, 'owner', true)).toBe('create')
    expect(accessOf(0o750, 'group', true)).toBe('access')
    expect(accessOf(0o744, 'others', true)).toBe('list')
    expect(withAccess(0o700, 'others', true, 'access')).toBe(0o705)
  })
})
