// Work to save before this browser locks. Locking clears the page (D40),
// and the server refuses a locked browser's requests, so an app with
// unsaved changes (the editor's drafts, D48) writes them out first.

const savers = new Set<() => Promise<void>>()

/** Runs fn before Lock Screen; returns the function that stops it. */
export function onBeforeLock(fn: () => Promise<void>): () => void {
  savers.add(fn)
  return () => savers.delete(fn)
}

/** Runs every saver, waiting at most timeoutMs: the lock must not hang. */
export async function saveBeforeLock(timeoutMs = 2000): Promise<void> {
  const all = Promise.allSettled([...savers].map((fn) => fn()))
  await Promise.race([all, new Promise((resolve) => setTimeout(resolve, timeoutMs))])
}
