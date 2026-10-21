// Folders on screen. The events stream sends this set to the worker, which
// watches them with inotify and reports changes as "fs" events; those reach
// the windows through emitFsChanged (lib/bus.ts). Several windows can show
// the same folder, so folders are counted.
import { onBeforeUnmount, watch } from 'vue'

type Sender = (dirs: string[]) => void

const refs = new Map<string, number>()
let sender: Sender | undefined
let queued = false

/** Watches dir until the returned function is called. */
export function watchDir(dir: string): () => void {
  const n = refs.get(dir) ?? 0
  refs.set(dir, n + 1)
  if (n === 0) queueSync()
  let released = false
  return () => {
    if (released) return
    released = true
    const left = (refs.get(dir) ?? 1) - 1
    if (left > 0) {
      refs.set(dir, left)
    } else {
      refs.delete(dir)
      queueSync()
    }
  }
}

/** Every watched folder, once each. */
export function watchedDirs(): string[] {
  return [...refs.keys()]
}

/**
 * Sets where the watched set goes (the events stream) and sends it there.
 * Changes are batched, so opening a window that watches two folders sends
 * one message.
 */
export function setWatchSender(fn: Sender | undefined): void {
  sender = fn
  queueSync()
}

function queueSync() {
  if (queued) return
  queued = true
  queueMicrotask(() => {
    queued = false
    sender?.(watchedDirs())
  })
}

/**
 * Keeps the folders a component shows watched while it is mounted. The
 * getter is reactive: when the component moves to another folder, the old
 * one is released. Empty strings are skipped.
 */
export function useWatchedDirs(dirs: () => string[]): void {
  let release: (() => void)[] = []
  const stop = watch(
    () => [...new Set(dirs().filter(Boolean))].sort().join('\n'),
    (key) => {
      const next = key ? key.split('\n').map(watchDir) : []
      release.forEach((r) => r())
      release = next
    },
    { immediate: true },
  )
  onBeforeUnmount(() => {
    stop()
    release.forEach((r) => r())
    release = []
  })
}
