import { api } from './client'
import type { HostApp } from './types'

/** The host's graphical apps, run through Xpra (D78). */
export const hostAppsApi = {
  /** `available`: Xpra and its HTML5 client are installed. */
  list: () => api.get<{ available: boolean; apps: HostApp[] }>('/api/v1/host-apps'),
  /** Starts the app in the user's Xpra session, starting that first if need be. */
  launch: (id: string) => api.post<void>('/api/v1/host-apps/launch', { id }),
  /** Starts the user's Xpra session, for the Host Apps window. */
  session: () => api.post<void>('/api/v1/host-apps/session', {}),
}
