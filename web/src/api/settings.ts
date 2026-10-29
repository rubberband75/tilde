import { api } from './client'
import type { DesktopTheme, LockSettings, LoginRecord, Settings } from './types'

/** Tilde's settings (D72), kept by the worker in ~/.config/tilde. */
export const settingsApi = {
  /** The settings, and what the desktop (Cinnamon, GNOME) says about locking. */
  get: () => api.get<{ settings: Settings; desktopLock: LockSettings; desktopTheme: DesktopTheme }>('/api/v1/settings'),
  /** Replaces them all; every browser is told. */
  save: (s: Settings) => api.put<Settings>('/api/v1/settings', s),
  /**
   * Recent logins and failed attempts on this account, from the audit log,
   * read by the server; `available` is false when it cannot read it.
   */
  logins: () => api.get<{ available: boolean; entries: LoginRecord[] }>('/api/auth/logins'),
}
