// Tiny event bus for "a directory changed" until the worker pushes inotify
// events over the control WebSocket (docs/roadmap.md, phase 4).

type Handler = (dir: string) => void
const fsHandlers = new Set<Handler>()

export function onFsChanged(fn: Handler): () => void {
  fsHandlers.add(fn)
  return () => fsHandlers.delete(fn)
}

export function emitFsChanged(...dirs: string[]): void {
  for (const d of new Set(dirs)) fsHandlers.forEach((fn) => fn(d))
}
