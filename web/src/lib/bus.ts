// Tiny event bus for "a directory changed" until the worker pushes inotify
// events over the control WebSocket (docs/roadmap.md, phase 4).

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
