// Edge tiling, as Cinnamon does it: dragging a window to a screen edge
// offers that half of the work area, a corner a quarter (D52). The
// geometry is pure so it can be tested on its own.
import type { Rect } from '@/stores/windows'

export const tiles = ['left', 'right', 'top', 'bottom', 'top-left', 'top-right', 'bottom-left', 'bottom-right'] as const
export type Tile = (typeof tiles)[number]

/** How close to an edge the pointer must come, in pixels. */
export const SNAP_EDGE = 8

export function isTile(v: unknown): v is Tile {
  return typeof v === 'string' && (tiles as readonly string[]).includes(v)
}

/** The part of the work area a tile covers. */
export function tileRect(tile: Tile, area: { w: number; h: number }): Rect {
  const halfW = Math.round(area.w / 2)
  const halfH = Math.round(area.h / 2)
  const cols = tile.endsWith('left') ? [0, halfW] : tile.endsWith('right') ? [halfW, area.w - halfW] : [0, area.w]
  const rows = tile.startsWith('top') ? [0, halfH] : tile.startsWith('bottom') ? [halfH, area.h - halfH] : [0, area.h]
  return { x: cols[0]!, y: rows[0]!, w: cols[1]!, h: rows[1]! }
}

/** The length of an edge's corner zones: a quarter of it, at most 160 px. */
function corner(length: number) {
  return Math.min(160, length / 4)
}

/**
 * The snap target under the pointer, or null away from the edges. The
 * pointer may be outside the work area (over the panel, or past the
 * browser window while the drag holds the pointer capture).
 */
export function snapTarget(x: number, y: number, area: { w: number; h: number }): Tile | null {
  const atLeft = x <= SNAP_EDGE
  const atRight = x >= area.w - 1 - SNAP_EDGE
  const atTop = y <= SNAP_EDGE
  if (atLeft || atRight) {
    const side = atLeft ? 'left' : 'right'
    if (y < corner(area.h)) return `top-${side}`
    if (y > area.h - corner(area.h)) return `bottom-${side}`
    return side
  }
  if (atTop) {
    if (x < corner(area.w)) return 'top-left'
    if (x > area.w - corner(area.w)) return 'top-right'
    return 'top'
  }
  return null
}

export type Direction = 'left' | 'right' | 'up' | 'down'

/** A tile as a cell in a 3 × 3 grid: column and row each -1, 0 (spanning) or 1. */
function cell(tile: Tile | undefined): [number, number] {
  if (!tile) return [0, 0]
  const col = tile.endsWith('left') ? -1 : tile.endsWith('right') ? 1 : 0
  const row = tile.startsWith('top') ? -1 : tile.startsWith('bottom') ? 1 : 0
  return [col, row]
}

function tileAt(col: number, row: number): Tile | undefined {
  const v = row === -1 ? 'top' : row === 1 ? 'bottom' : ''
  const h = col === -1 ? 'left' : col === 1 ? 'right' : ''
  const name = v && h ? `${v}-${h}` : v || h
  return isTile(name) ? name : undefined
}

/**
 * Keyboard tiling (Super+arrows, D53), as Muffin pushes tiles: a key moves
 * the window one step that way in a grid of halves and quarters, so Left
 * then Up gives the top-left quarter and Right from the left half gives a
 * plain window again. Up from a plain window or the top half maximizes;
 * Down from maximized goes back to what it was. undefined means untiled.
 */
export function pushTile(
  tile: Tile | undefined,
  maximized: boolean,
  dir: Direction,
): { tile: Tile | undefined; maximized: boolean } {
  if (maximized) {
    if (dir === 'left' || dir === 'right') return { tile: dir, maximized: false }
    return { tile, maximized: dir === 'up' }
  }
  let [col, row] = cell(tile)
  if (dir === 'left') col = col === 1 ? 0 : -1
  else if (dir === 'right') col = col === -1 ? 0 : 1
  else if (dir === 'up') {
    if (col === 0 && row <= 0) return { tile, maximized: true }
    row = row === 1 ? 0 : -1
  } else row = row === -1 ? 0 : 1
  return { tile: tileAt(col, row), maximized: false }
}
