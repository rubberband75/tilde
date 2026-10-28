// Unsaved documents to recover (D58): drafts the worker keeps (D48) that
// no editor window in this browser shows. A draft of a file comes back by
// opening the file; one of a document never saved has no other way back.
import { dirName } from '@/api/fs'
import type { DraftInfo } from '@/api/types'
import { tabKey, tabsFromArgs } from './tabs'

/** The draft keys editor windows show, from their saved state: every tab (D63). */
export function shownDraftKeys(windows: ReadonlyArray<Readonly<Record<string, unknown>>>): Set<string> {
  const keys = new Set<string>()
  for (const args of windows) {
    for (const t of tabsFromArgs(args).tabs) {
      // A window without saved tabs gets a fresh one: nothing it shows yet.
      if (t.path || Array.isArray(args.tabs) || typeof args.draft === 'string') keys.add(tabKey(t))
    }
  }
  return keys
}

export interface Recoverable {
  key: string
  /** The file's name, or the first line of a document never saved. */
  name: string
  /** The file's folder, or that it was never saved. */
  where: string
  updated: string
  /** Launch arguments for an editor window showing it. */
  args: { path: string } | { draft: string }
}

/** The drafts no window shows, newest first, as the list names them. */
export function recoverable(drafts: DraftInfo[], shown: Set<string>): Recoverable[] {
  const out: Recoverable[] = []
  for (const d of drafts) {
    if (shown.has(d.key)) continue
    if (d.key.startsWith('file:')) {
      const path = d.key.slice('file:'.length)
      out.push({
        key: d.key,
        name: path.split('/').pop() || path,
        where: dirName(path),
        updated: d.updated,
        args: { path },
      })
    } else if (d.key.startsWith('new:')) {
      out.push({
        key: d.key,
        name: d.preview || 'Untitled Document',
        where: 'Never saved',
        updated: d.updated,
        args: { draft: d.key.slice('new:'.length) },
      })
    }
  }
  return out
}
