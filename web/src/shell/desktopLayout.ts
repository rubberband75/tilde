// Desktop icon placement on a grid of cells (D36). Saved positions win when
// they fit the window and the cell is free; everything else fills the first
// free cell, top to bottom, then left to right, as on Nemo's desktop. A
// window too small for a saved position places the icon elsewhere for now
// but keeps the saved position for when the window grows.
import type { Cell, DesktopLayout } from '@/api/types'

export interface Grid {
  cols: number
  rows: number
}

const cellKey = (c: Cell) => `${c.col},${c.row}`

export function inGrid(c: Cell, grid: Grid): boolean {
  return c.col >= 0 && c.row >= 0 && c.col < grid.cols && c.row < grid.rows
}

/** Places every key: wanted cells first (in key order), then the first free cells. */
export function placeIcons(keys: string[], wanted: Record<string, Cell>, grid: Grid): Map<string, Cell> {
  const out = new Map<string, Cell>()
  const taken = new Set<string>()
  for (const k of keys) {
    const c = wanted[k]
    if (c && inGrid(c, grid) && !taken.has(cellKey(c))) {
      out.set(k, c)
      taken.add(cellKey(c))
    }
  }
  const rows = Math.max(1, grid.rows)
  let next = 0
  for (const k of keys) {
    if (out.has(k)) continue
    let c: Cell
    // Past the last cell of the window, keep going in columns to the right.
    do {
      c = { col: Math.floor(next / rows), row: next % rows }
      next++
    } while (taken.has(cellKey(c)))
    out.set(k, c)
    taken.add(cellKey(c))
  }
  return out
}

/** The free cell of the grid nearest to c (ties: leftmost, then topmost); c itself if none is free. */
export function nearestFree(c: Cell, taken: Set<string>, grid: Grid): Cell {
  let best: Cell | undefined
  let bestD = Infinity
  for (let col = 0; col < grid.cols; col++) {
    for (let row = 0; row < grid.rows; row++) {
      if (taken.has(`${col},${row}`)) continue
      const d = (col - c.col) ** 2 + (row - c.row) ** 2
      if (d < bestD) {
        bestD = d
        best = { col, row }
      }
    }
  }
  return best ?? c
}

/**
 * Moves icons so that anchor lands on target, keeping the others where they
 * were relative to it, as a drag of several icons does in Nemo. Icons that
 * would leave the grid or land on another icon go to the nearest free cell.
 */
export function moveIcons(
  layout: Map<string, Cell>,
  moved: string[],
  anchor: string,
  target: Cell,
  grid: Grid,
): Map<string, Cell> {
  const from = layout.get(anchor)
  if (!from) return layout
  const out = new Map(layout)
  const taken = new Set<string>()
  for (const [k, c] of layout) if (!moved.includes(k)) taken.add(cellKey(c))
  for (const k of [anchor, ...moved.filter((m) => m !== anchor)]) {
    const c = layout.get(k)
    if (!c) continue
    let to = { col: target.col + c.col - from.col, row: target.row + c.row - from.row }
    if (!inGrid(to, grid) || taken.has(cellKey(to))) to = nearestFree(to, taken, grid)
    out.set(k, to)
    taken.add(cellKey(to))
  }
  return out
}

/**
 * What to save after a move: where every present icon is now, except icons
 * whose saved position is outside this (smaller) window, which keep it.
 * Icons that are gone are dropped.
 */
export function positionsToSave(
  layout: Map<string, Cell>,
  saved: Record<string, Cell>,
  grid: Grid,
  moved: string[],
): Record<string, Cell> {
  const out: Record<string, Cell> = {}
  for (const [k, c] of layout) {
    const s = saved[k]
    out[k] = s && !inGrid(s, grid) && !moved.includes(k) ? s : c
  }
  return out
}

/**
 * Turns Nemo's pixel positions into cells. The cell size is Nemo's grid
 * size or the smallest gap between icons, whichever is larger, counted from
 * the top-left-most icon, so the arrangement keeps its shape on a
 * differently sized window.
 */
export function cellsFromNemo(nemo: DesktopLayout['nemo']): Record<string, Cell> {
  const icons = Object.entries(nemo?.icons ?? {})
  if (!icons.length) return {}
  const axis = (vals: number[], grid?: number) => {
    const sorted = [...new Set(vals)].sort((a, b) => a - b)
    let gap = 0
    for (let i = 1; i < sorted.length; i++) {
      const d = sorted[i]! - sorted[i - 1]!
      if (d > 8 && (gap === 0 || d < gap)) gap = d
    }
    return { min: sorted[0]!, pitch: Math.max(gap, grid ?? 0, 1) }
  }
  const x = axis(
    icons.map(([, p]) => p.x),
    nemo?.grid?.[0],
  )
  const y = axis(
    icons.map(([, p]) => p.y),
    nemo?.grid?.[1],
  )
  return Object.fromEntries(
    icons.map(([k, p]) => [k, { col: Math.round((p.x - x.min) / x.pitch), row: Math.round((p.y - y.min) / y.pitch) }]),
  )
}
