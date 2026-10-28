import { api } from './client'
import type { JournalEntry, Service, ServiceAction, ServiceScope } from './types'

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
  /**
   * The last 200 journal lines, oldest first, read as the user: `limited`
   * when journald held back what they may not read (not in adm or
   * systemd-journal).
   */
  journal: (scope: ServiceScope, name: string) =>
    api.get<{ entries: JournalEntry[]; limited: boolean }>('/api/v1/services/journal', { scope, name }),
  /** Enables or disables a service at boot; resolves with its unit file's state now. */
  setBoot: (scope: ServiceScope, name: string, enabled: boolean) =>
    api.post<{ boot: string }>('/api/v1/services/boot', { scope, name, enabled }),
}
