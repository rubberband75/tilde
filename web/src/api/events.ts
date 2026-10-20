// The events WebSocket: the worker pushes job progress (and, in phase 4,
// filesystem changes) instead of the browser polling. It reconnects with
// backoff; every connection starts with a full snapshot, so nothing is lost
// while it was down.
import { api, wsUrl } from './client'
import type { WorkerEvent } from './types'

export interface EventStream {
  close(): void
}

export function connectEvents(onEvent: (ev: WorkerEvent) => void): EventStream {
  let ws: WebSocket | undefined
  let closed = false
  let retries = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  function open() {
    if (closed) return
    const socket = new WebSocket(wsUrl('/api/v1/events'))
    ws = socket
    socket.onopen = () => {
      retries = 0
    }
    socket.onmessage = (e) => {
      if (typeof e.data !== 'string') return
      try {
        onEvent(JSON.parse(e.data) as WorkerEvent)
      } catch {
        // ignore malformed frames
      }
    }
    socket.onclose = () => {
      if (closed || ws !== socket) return
      const delay = Math.min(10_000, 500 * 2 ** retries++)
      timer = setTimeout(() => {
        // A request first: if the session has ended, the 401 sends the
        // shell back to the login screen, which closes this stream.
        api.get('/api/v1/hello').then(open, () => !closed && open())
      }, delay)
    }
  }

  open()
  return {
    close() {
      closed = true
      clearTimeout(timer)
      ws?.close()
    },
  }
}
