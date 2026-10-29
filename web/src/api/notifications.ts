import { api } from './client'
import type { NotificationsState } from './types'

/** Programs' notifications (D70), kept by the worker. */
export const notificationsApi = {
  list: () => api.get<NotificationsState>('/api/v1/notifications'),
  /** A click on a button, or on the notification itself ("default"). */
  action: (id: number, key: string) => api.post<void>('/api/v1/notifications/action', { id, key }),
  /** Dismisses the toast; it stays in the list. */
  close: (id: number) => api.post<void>('/api/v1/notifications/close', { id }),
  /** Removes notifications from the list: those given, or all. */
  clear: (ids?: number[]) => api.post<void>('/api/v1/notifications/clear', { ids }),
  /** The picture a program sent with notification `id`. */
  imageUrl: (id: number) => `/api/v1/notifications/image?id=${id}`,
}
