// Who wants resource snapshots (D61): the panel applet and System
// Monitor's Resources tab each hold a watch while shown. The events stream
// tells the worker when the first starts and the last stops, so it samples
// only while something shows the figures.
import { onBeforeUnmount, onMounted } from 'vue'

type Sender = (on: boolean) => void

let watchers = 0
let sender: Sender | undefined
let sent: boolean | undefined

function sync() {
  const on = watchers > 0
  if (on === sent || !sender) return
  sent = on
  sender(on)
}

/** Adds a watch; returns the function that removes it. */
export function watchResources(): () => void {
  watchers++
  sync()
  let done = false
  return () => {
    if (done) return
    done = true
    watchers--
    sync()
  }
}

export function resourcesWatched(): boolean {
  return watchers > 0
}

/**
 * Sets where the watch goes (the events stream) and sends the current one
 * there: a new connection starts unwatched.
 */
export function setResourcesSender(fn: Sender | undefined): void {
  sender = fn
  sent = undefined
  if (fn && watchers > 0) sync()
}

/** Keeps resource snapshots coming while the component is mounted. */
export function useResourcesWatch(): void {
  let stop: (() => void) | undefined
  onMounted(() => {
    stop = watchResources()
  })
  onBeforeUnmount(() => stop?.())
}
