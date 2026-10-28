import { api } from './client'
import type { ConnectResult, NetworkState } from './types'

/** NetworkManager through the worker (D55). Changes may wait for a polkit password dialog. */
export const networkApi = {
  state: () => api.get<NetworkState>('/api/v1/network'),
  setWifi: (enabled: boolean) => api.post<void>('/api/v1/network/wifi', { enabled }),
  scan: (device: string) => api.post<void>('/api/v1/network/scan', { device }),
  /**
   * Brings a device up, on the Wi-Fi network ssid. Rejects with
   * `password_required` when a new secured network needs one, and
   * `unsupported` for security Tilde cannot set up.
   */
  connect: (device: string, ssid?: string, password?: string) =>
    api.post<ConnectResult>('/api/v1/network/connect', { device, ssid, password }),
  disconnect: (device: string) => api.post<void>('/api/v1/network/disconnect', { device }),
}
