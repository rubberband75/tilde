import { describe, expect, it } from 'vitest'
import { percent, rate, share } from './rates'

describe('rates', () => {
  it('words figures', () => {
    expect([0, 3.14, 12.6, 100].map(percent)).toEqual(['0.0%', '3.1%', '13%', '100%'])
    expect(rate(0)).toMatch(/\/s$/)
    expect(share(1, 4)).toBe(25)
    expect(share(1, 0)).toBe(0)
  })
})
