// Drag and drop of files between Files windows, the desktop and terminals.
// The paths travel in our own MIME type (and as text, so dropping on another
// app pastes them). Files dragged in from the user's computer arrive as
// dataTransfer.files instead and are uploaded.
import type { JobOp } from '@/api/types'

const TYPE = 'application/x-tilde-paths'

export function setDragPaths(e: DragEvent, paths: string[]): void {
  const dt = e.dataTransfer
  if (!dt) return
  dt.setData(TYPE, JSON.stringify(paths))
  dt.setData('text/plain', paths.join('\n'))
  dt.effectAllowed = 'copyMove'
}

/** True while dragging Tilde paths (the data itself is readable only on drop). */
export function isPathDrag(e: DragEvent): boolean {
  return !!e.dataTransfer?.types.includes(TYPE)
}

export function dragPaths(e: DragEvent): string[] {
  try {
    const v: unknown = JSON.parse(e.dataTransfer?.getData(TYPE) || '[]')
    return Array.isArray(v) ? v.filter((p): p is string => typeof p === 'string') : []
  } catch {
    return []
  }
}

/**
 * The job for a drop, as in Nemo: Ctrl copies, Shift moves, otherwise move
 * within a filesystem and copy across ("drop", decided by the worker).
 */
export function dropOp(e: DragEvent): JobOp {
  if (e.ctrlKey) return 'copy'
  if (e.shiftKey) return 'move'
  return 'drop'
}

/** Sets the cursor feedback during dragover. */
export function setDropEffect(e: DragEvent): void {
  if (e.dataTransfer) e.dataTransfer.dropEffect = e.ctrlKey ? 'copy' : 'move'
}

/** Drops that would do nothing: onto one of the dragged items, or back into the folder they are in. */
export function isNoopDrop(paths: string[], dest: string): boolean {
  return paths.some((p) => p === dest) || paths.every((p) => p.slice(0, p.lastIndexOf('/') || 1) === dest)
}

/** Quotes paths for a POSIX shell, for dropping onto a terminal. */
export function shellQuote(paths: string[]): string {
  return paths.map((p) => (/^[\w@%+=:,./-]+$/.test(p) ? p : `'${p.replace(/'/g, `'\\''`)}'`)).join(' ')
}
