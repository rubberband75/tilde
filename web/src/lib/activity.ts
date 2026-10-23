// Reports keyboard and pointer input to the server, which locks this
// browser once the user's idle delay passes without any (D40). The server
// keeps the clock, not the page: tabs of one browser share its cookie, so
// the server sees their input together, and a lock reaches every tab as
// closed sockets and 423 answers. A report goes out on the first input
// after a quiet spell, then at most once per heartbeat while input goes on.
import type { LockTiming } from '@/api/types'

export interface ActivityReporter {
  /** Records input: reports it now, or as soon as the heartbeat allows. */
  input(): void
  stop(): void
}

/**
 * send reports input and answers with the current timing; heartbeat is the
 * starting interval in seconds.
 */
export function createReporter(
  send: () => Promise<LockTiming>,
  heartbeat: number,
  now: () => number = Date.now,
): ActivityReporter {
  let interval = Math.max(1, heartbeat) * 1000
  let last = -Infinity
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  function report() {
    timer = undefined
    if (stopped) return
    last = now()
    send().then(
      (t) => {
        interval = Math.max(1, t.heartbeat) * 1000
      },
      () => {
        // A lock or logout is handled by the API client's listeners.
      },
    )
  }

  return {
    input() {
      if (stopped || timer !== undefined) return
      const wait = last + interval - now()
      if (wait <= 0) report()
      else timer = setTimeout(report, wait)
    },
    stop() {
      stopped = true
      clearTimeout(timer)
      timer = undefined
    },
  }
}

const inputEvents = ['keydown', 'pointerdown', 'pointermove', 'wheel', 'touchstart'] as const

/** Feeds input anywhere in the page to reporter; the result removes the listeners. */
export function trackInput(reporter: ActivityReporter): () => void {
  const onInput = () => reporter.input()
  // Capture: apps that stop propagation (terminals, menus) still count.
  const opts = { capture: true, passive: true }
  for (const type of inputEvents) window.addEventListener(type, onInput, opts)
  return () => {
    for (const type of inputEvents) window.removeEventListener(type, onInput, opts)
    reporter.stop()
  }
}
