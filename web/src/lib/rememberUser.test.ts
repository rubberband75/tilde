import { afterEach, describe, expect, it, vi } from 'vitest'
import { rememberedUser, rememberUser } from './rememberUser'

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('remember user name', () => {
  it('keeps the name only when asked, and forgets it', () => {
    expect(rememberedUser()).toBe('')
    rememberUser('alice', false)
    expect(rememberedUser()).toBe('')
    rememberUser('alice', true)
    expect(rememberedUser()).toBe('alice')
    rememberUser('alice', false)
    expect(rememberedUser()).toBe('')
  })

  it('keeps nothing but the name', () => {
    rememberUser('alice', true)
    expect(Object.keys(localStorage)).toEqual(['tilde:login-user'])
  })

  it('works without storage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(() => rememberUser('alice', true)).not.toThrow()
    expect(rememberedUser()).toBe('')
  })
})
