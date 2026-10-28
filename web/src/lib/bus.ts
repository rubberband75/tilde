// Tiny event bus between windows. "A folder changed" comes from the worker's
// inotify events (lib/fswatch.ts) and from the shell's own file operations,
// which announce their result at once instead of waiting for the event.

type Handler = (dir: string) => void
const fsHandlers = new Set<Handler>()

export function onFsChanged(fn: Handler): () => void {
  fsHandlers.add(fn)
  return () => fsHandlers.delete(fn)
}

const bookmarkHandlers = new Set<() => void>()

/** Bookmarks changed in one window; the others reload their sidebar. */
export function onBookmarksChanged(fn: () => void): () => void {
  bookmarkHandlers.add(fn)
  return () => bookmarkHandlers.delete(fn)
}

export function emitBookmarksChanged(): void {
  bookmarkHandlers.forEach((fn) => fn())
}

export function emitFsChanged(...dirs: string[]): void {
  for (const d of new Set(dirs)) fsHandlers.forEach((fn) => fn(d))
}

const driveHandlers = new Set<() => void>()

/** Drives or volumes changed (the worker's "drives" event, or an eject here). */
export function onDrivesChanged(fn: () => void): () => void {
  driveHandlers.add(fn)
  return () => driveHandlers.delete(fn)
}

export function emitDrivesChanged(): void {
  driveHandlers.forEach((fn) => fn())
}

const networkHandlers = new Set<() => void>()

/** NetworkManager's devices or connections changed (the worker's "network" event). */
export function onNetworkChanged(fn: () => void): () => void {
  networkHandlers.add(fn)
  return () => networkHandlers.delete(fn)
}

export function emitNetworkChanged(): void {
  networkHandlers.forEach((fn) => fn())
}
