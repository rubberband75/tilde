import { api, wsUrl } from './client'
import type { HostApp } from './types'
import { connectWebSocket } from '@/xpra/websocket'

/** The host's graphical apps, run through Xpra (D78). */
export const hostAppsApi = {
  /** `available`: Xpra and its HTML5 client are installed. */
  list: () => api.get<{ available: boolean; apps: HostApp[] }>('/api/v1/host-apps'),
  /** Starts the app in the user's Xpra session, starting that first if need be. */
  launch: (id: string) => api.post<void>('/api/v1/host-apps/launch', { id }),
  /** Starts the user's Xpra session, for the Host Apps window. */
  session: () => api.post<void>('/api/v1/host-apps/session', {}),
  /**
   * A byte stream to the running session, for Tilde's own Xpra client
   * (D80); it does not start the session.
   */
  connect: () => connectWebSocket(wsUrl('/api/v1/host-apps/connect')),
}
