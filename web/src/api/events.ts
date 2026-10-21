// The events WebSocket: the worker pushes job progress and changes to the
// folders the client watches, instead of the browser polling. It reconnects
// with backoff; every connection starts with a full job snapshot, and
// onOpen lets the caller resend its watches and refresh what it may have
// missed while it was down.
import { api, wsUrl } from './client'
import type { ClientMessage, WorkerEvent } from './types'

export interface EventStream {
  /** Sends a message if connected; onOpen covers the gaps. */
  send(msg: ClientMessage): void
  close(): void
}

export function connectEvents(
  onEvent: (ev: WorkerEvent) => void,
  onOpen?: (reconnected: boolean) => void,
): EventStream {
  let opened = false
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
      onOpen?.(opened)
      opened = true
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
    send(msg) {
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg))
    },
    close() {
      closed = true
      clearTimeout(timer)
      ws?.close()
    },
  }
}
