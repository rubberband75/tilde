import type { Entry, TrashItem, Volume } from '@/api/types'

/** One thing shown in a Files view: a file or folder, a trash item or a drive. */
export interface Item {
  key: string
  name: string
  icon: string[]
  /** Human type label ("Folder", "PNG image"). */
  type: string
  size?: number
  /** ISO time shown in the date column (modified, or deleted for trash). */
  mtime?: string
  /** mtime as milliseconds, for sorting. */
  mtimeMs: number
  /** Folder the item is in, shown for search results. */
  location?: string
  entry?: Entry
  trash?: TrashItem
  volume?: Volume
  /** Shown dimmed: hidden files, volumes that are not mounted. */
  dim?: boolean
}

export type SortKey = 'name' | 'size' | 'type' | 'mtime'
export const sortKeys: SortKey[] = ['name', 'size', 'type', 'mtime']

export type ViewMode = 'grid' | 'list'

/** Icon sizes per zoom level, as Nemo's zoom steps. */
export const zoomLevels: Record<ViewMode, number[]> = {
  grid: [32, 48, 64, 96, 128],
  list: [16, 24, 32],
}
export const defaultZoom: Record<ViewMode, number> = { grid: 1, list: 0 }

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/**
 * Sorts like Nemo: folders first (also when reversed), natural order for
 * names ("file2" before "file10"), ties broken by name.
 */
export function sortItems(items: Item[], key: SortKey, desc: boolean): Item[] {
  const dir = desc ? -1 : 1
  return [...items].sort((a, b) => {
    const ad = a.entry?.type === 'dir'
    const bd = b.entry?.type === 'dir'
    if (ad !== bd) return ad ? -1 : 1
    let c = 0
    if (key === 'size') c = (a.size ?? 0) - (b.size ?? 0)
    else if (key === 'type') c = collator.compare(a.type, b.type)
    else if (key === 'mtime') c = a.mtimeMs - b.mtimeMs
    return (c || collator.compare(a.name, b.name)) * dir
  })
}
