// Edge tiling, as Cinnamon does it: dragging a window to a screen edge
// offers that half of the work area, a corner a quarter (D52). The
// geometry is pure so it can be tested on its own.
import type { Rect } from '@/stores/windows'

export const tiles = ['left', 'right', 'top', 'top-left', 'top-right', 'bottom-left', 'bottom-right'] as const
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
