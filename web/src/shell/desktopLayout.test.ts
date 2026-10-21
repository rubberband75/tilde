import { describe, expect, it } from 'vitest'
import type { Cell } from '@/api/types'
import { cellsFromNemo, moveIcons, nearestFree, placeIcons, positionsToSave } from './desktopLayout'

const grid = { cols: 4, rows: 3 }
const obj = (m: Map<string, Cell>) => Object.fromEntries(m)

describe('placeIcons', () => {
  it('fills columns top to bottom when nothing is saved', () => {
    expect(obj(placeIcons(['a', 'b', 'c', 'd'], {}, grid))).toEqual({
      a: { col: 0, row: 0 },
      b: { col: 0, row: 1 },
      c: { col: 0, row: 2 },
      d: { col: 1, row: 0 },
    })
  })

  it('keeps saved cells and flows the rest around them', () => {
    const placed = placeIcons(['a', 'b', 'c'], { b: { col: 0, row: 0 }, c: { col: 3, row: 2 } }, grid)
    expect(obj(placed)).toEqual({ a: { col: 0, row: 1 }, b: { col: 0, row: 0 }, c: { col: 3, row: 2 } })
  })

  it('places icons whose saved cell is outside the window or taken', () => {
    const placed = placeIcons(
      ['a', 'b', 'c'],
      { a: { col: 9, row: 0 }, b: { col: 0, row: 0 }, c: { col: 0, row: 0 } },
      grid,
    )
    expect(obj(placed)).toEqual({ a: { col: 0, row: 1 }, b: { col: 0, row: 0 }, c: { col: 0, row: 2 } })
  })

  it('overflows to the right when the window is full', () => {
    const placed = placeIcons(['a', 'b', 'c'], {}, { cols: 1, rows: 2 })
    expect(placed.get('c')).toEqual({ col: 1, row: 0 })
  })
})

describe('moveIcons', () => {
  const layout = placeIcons(['a', 'b', 'c', 'd'], {}, grid) // a b c in column 0, d at 1,0

  it('moves one icon to a free cell', () => {
    expect(moveIcons(layout, ['a'], 'a', { col: 3, row: 1 }, grid).get('a')).toEqual({ col: 3, row: 1 })
  })

  it('keeps a group in shape around the dragged icon', () => {
    const moved = moveIcons(layout, ['a', 'b'], 'b', { col: 2, row: 2 }, grid)
    // b lands on 2,2; a was one above it.
    expect(moved.get('b')).toEqual({ col: 2, row: 2 })
    expect(moved.get('a')).toEqual({ col: 2, row: 1 })
    expect(moved.get('c')).toEqual({ col: 0, row: 2 })
  })

  it('sends icons that would collide or leave the grid to the nearest free cell', () => {
    // Onto d: 1,1 and 2,0 are equally near; the leftmost wins.
    const onD = moveIcons(layout, ['c'], 'c', { col: 1, row: 0 }, grid)
    expect(onD.get('c')).toEqual({ col: 1, row: 1 })
    expect(onD.get('d')).toEqual({ col: 1, row: 0 })
    const off = moveIcons(layout, ['b', 'c'], 'b', { col: 3, row: 2 }, grid)
    expect(off.get('b')).toEqual({ col: 3, row: 2 })
    expect(off.get('c')).toEqual({ col: 2, row: 2 }) // 3,3 is below the grid
  })

  it('lets an icon take a cell another moved icon leaves', () => {
    const moved = moveIcons(layout, ['a', 'b'], 'a', { col: 0, row: 1 }, grid)
    expect(moved.get('a')).toEqual({ col: 0, row: 1 })
    expect(moved.get('b')).toEqual({ col: 1, row: 2 }) // 0,2 is c's, so the nearest free cell
  })
})

describe('nearestFree', () => {
  it('prefers the closest cell', () => {
    expect(nearestFree({ col: 5, row: 1 }, new Set(['3,1']), grid)).toEqual({ col: 3, row: 0 })
  })
})

describe('positionsToSave', () => {
  it('keeps saved cells that only this small window cannot show', () => {
    const layout = new Map([
      ['a', { col: 0, row: 0 }],
      ['b', { col: 0, row: 1 }],
    ])
    const saved = { a: { col: 7, row: 7 }, b: { col: 9, row: 9 }, gone: { col: 1, row: 1 } }
    expect(positionsToSave(layout, saved, grid, ['b'])).toEqual({ a: { col: 7, row: 7 }, b: { col: 0, row: 1 } })
  })
})

describe('cellsFromNemo', () => {
  it('maps a Nemo column of icons and a second column', () => {
    const cells = cellsFromNemo({
      grid: [56, 91],
      icons: {
        'computer:///': { x: 32, y: 20 },
        'trash:///': { x: 32, y: 111 },
        Plex: { x: 32, y: 202 },
        far: { x: 32 + 3 * 100, y: 20 + 91 },
      },
    })
    expect(cells).toEqual({
      'computer:///': { col: 0, row: 0 },
      'trash:///': { col: 0, row: 1 },
      Plex: { col: 0, row: 2 },
      far: { col: 1, row: 1 }, // the only x gap (300 px) is the pitch
    })
  })

  it('handles nothing to seed from', () => {
    expect(cellsFromNemo(undefined)).toEqual({})
    expect(cellsFromNemo({ icons: {} })).toEqual({})
  })
})
