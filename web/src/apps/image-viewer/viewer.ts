// What the image viewer works out without the DOM: which files it can
// show, their order in the folder, and the zoom steps (after xviewer's).
import type { Entry } from '@/api/types'
import { folderSiblings } from '@/lib/folderSiblings'

export { neighbour } from '@/lib/folderSiblings'

/** Image types every supported browser draws in an <img>. */
export const viewableImageTypes = [
  'image/png',
  'image/apng',
  'image/jpeg',
  'image/pjpeg',
  'image/gif',
  'image/webp',
  'image/avif',
  'image/bmp',
  'image/x-bmp',
  'image/x-ms-bmp',
  'image/svg+xml',
  'image/vnd.microsoft.icon',
  'image/x-icon',
]

export function isViewable(entry: Pick<Entry, 'type' | 'mime'>): boolean {
  return entry.type === 'file' && viewableImageTypes.includes(entry.mime)
}

/** The images of a folder the viewer steps through, in Files' name order. */
export function folderImages(entries: readonly Entry[], current: string): Entry[] {
  return folderSiblings(entries, current, isViewable)
}

/** Zoom steps, as xviewer offers them. */
export const zoomSteps = [0.05, 0.1, 0.2, 0.33, 0.5, 0.67, 1, 1.33, 1.5, 2, 3, 4, 5, 6, 8, 10, 15, 20]
export const minZoom = zoomSteps[0]!
export const maxZoom = zoomSteps[zoomSteps.length - 1]!

/** The next step in (dir 1) or out (dir -1) from zoom, which may lie between steps. */
export function stepZoom(zoom: number, dir: 1 | -1): number {
  const eps = 1e-6
  if (dir > 0) return zoomSteps.find((z) => z > zoom + eps) ?? maxZoom
  return [...zoomSteps].reverse().find((z) => z < zoom - eps) ?? minZoom
}

export function clampZoom(zoom: number): number {
  return Math.min(maxZoom, Math.max(minZoom, zoom))
}

/**
 * Best Fit: the whole image in the view, never enlarged past its own size.
 * quarterTurns is the rotation; an odd one swaps width and height.
 */
export function fitZoom(image: { w: number; h: number }, view: { w: number; h: number }, quarterTurns: number): number {
  if (!image.w || !image.h || view.w <= 0 || view.h <= 0) return 1
  const turned = quarterTurns % 2 !== 0
  const w = turned ? image.h : image.w
  const h = turned ? image.w : image.h
  return clampZoom(Math.min(1, view.w / w, view.h / h))
}
