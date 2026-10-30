// The browser's way to the user's Xpra session (D80): a WebSocket to the
// worker, which relays its bytes to the session's socket.

import type { Transport } from './client'

/**
 * Opens a WebSocket to url and resolves once it is open. It rejects if the
 * worker refuses (no session running: the browser is not told why).
 */
export function connectWebSocket(url: string): Promise<Transport> {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url)
    socket.binaryType = 'arraybuffer'
    const t: Transport = {
      send: (b) => socket.send(b),
      close: () => socket.close(1000),
      onData: null,
      onClose: null,
    }
    let open = false
    socket.onopen = () => {
      open = true
      resolve(t)
    }
    socket.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) t.onData?.(new Uint8Array(e.data))
    }
    socket.onclose = (e) => {
      if (!open) reject(new Error('could not connect to the host apps session'))
      else t.onClose?.(e.reason || 'the connection to the host apps session was lost')
    }
  })
}
