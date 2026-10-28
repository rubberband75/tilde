import { api } from './client'
import type { Service, ServiceAction, ServiceScope } from './types'

/** systemd's services through the worker (D60): the system's, or the user's own. */
export const servicesApi = {
  /** `available` is false without that systemd manager (no session bus, no systemd). */
  list: (scope: ServiceScope) => api.get<{ available: boolean; services: Service[] }>('/api/v1/services', { scope }),
  /**
   * Starts, stops or restarts a service and resolves with its state once
   * settled. System services ask polkit, which may want a password
   * (Tilde's dialog); `403 not_authorized` if refused.
   */
  act: (scope: ServiceScope, name: string, action: ServiceAction) =>
    api.post<{ active: string; sub: string }>('/api/v1/services/action', { scope, name, action }),
}
