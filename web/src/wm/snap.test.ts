import { describe, expect, it } from 'vitest'
import { isTile, snapTarget, tileRect, tiles } from './snap'

const area = { w: 1281, h: 760 }

describe('snapTarget', () => {
  it.each([
    [640, 400, null],
    [0, 400, 'left'],
    [-30, 400, 'left'],
    [1280, 400, 'right'],
    [2000, 400, 'right'],
    [640, 0, 'top'],
    [640, -50, 'top'],
    [0, 10, 'top-left'],
    [20, 0, 'top-left'],
    [1280, 50, 'top-right'],
    [1200, 0, 'top-right'],
    [0, 700, 'bottom-left'],
    [0, 790, 'bottom-left'], // over the panel
    [1280, 759, 'bottom-right'],
    [9, 400, null],
    [640, 9, null],
  ])('at (%i, %i) is %s', (x, y, want) => {
    expect(snapTarget(x, y, area)).toBe(want)
  })

  it('keeps the corner zones inside small screens', () => {
    const small = { w: 400, h: 300 }
    expect(snapTarget(0, 74, small)).toBe('top-left')
    expect(snapTarget(0, 76, small)).toBe('left')
    expect(snapTarget(99, 0, small)).toBe('top-left')
    expect(snapTarget(101, 0, small)).toBe('top')
  })
})

describe('tileRect', () => {
  it('splits the work area without gaps or overlap', () => {
    expect(tileRect('left', area)).toEqual({ x: 0, y: 0, w: 641, h: 760 })
    expect(tileRect('right', area)).toEqual({ x: 641, y: 0, w: 640, h: 760 })
    expect(tileRect('top', area)).toEqual({ x: 0, y: 0, w: 1281, h: 380 })
    expect(tileRect('top-left', area)).toEqual({ x: 0, y: 0, w: 641, h: 380 })
    expect(tileRect('bottom-right', area)).toEqual({ x: 641, y: 380, w: 640, h: 380 })
    const odd = { w: 1001, h: 701 }
    const tl = tileRect('top-left', odd)
    const br = tileRect('bottom-right', odd)
    expect(tl.x + tl.w).toBe(br.x)
    expect(tl.y + tl.h).toBe(br.y)
    expect(br.x + br.w).toBe(odd.w)
    expect(br.y + br.h).toBe(odd.h)
  })
})

describe('isTile', () => {
  it('accepts tiles only', () => {
    for (const t of tiles) expect(isTile(t)).toBe(true)
    expect(isTile('maximize')).toBe(false)
    expect(isTile(3)).toBe(false)
    expect(isTile(undefined)).toBe(false)
  })
})
