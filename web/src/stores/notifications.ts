import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { notificationsApi } from '@/api/notifications'
import type { Notification } from '@/api/types'

/** At most this many toasts at once, the newest; the rest wait in the applet. */
export const maxToasts = 3

/**
 * Programs' notifications (D70), kept in sync over the events socket:
 * every browser shows the same toasts, and closing one closes it in all.
 */
export const useNotifications = defineStore('notifications', () => {
  /** Programs' notifications come to Tilde. */
  const active = ref(false)
  /** Newest first. */
  const items = ref<Notification[]>([])
  const toasts = computed(() => items.value.filter((n) => n.open).slice(0, maxToasts))
  let seq = 0
  let loading: Promise<void> | null = null
  let again = false

  /** Fetches the list; a burst of events makes one more fetch, not one each. */
  async function refresh(): Promise<void> {
    if (loading) {
      again = true
      return loading
    }
    const n = ++seq
    loading = (async () => {
      try {
        do {
          again = false
          const state = await notificationsApi.list()
          if (n !== seq) return
          active.value = state.active
          items.value = state.notifications
        } while (again)
      } catch {
        // The next event or reconnect brings the list.
      } finally {
        loading = null
      }
    })()
    return loading
  }

  function patch(id: number, change: (n: Notification) => Notification | null) {
    items.value = items.value.flatMap((n) => {
      if (n.id !== id) return [n]
      const m = change(n)
      return m ? [m] : []
    })
  }

  /** Clicks a button, or the notification itself with "default". */
  async function invoke(id: number, key: string) {
    patch(id, (n) => ({ ...n, open: false }))
    try {
      await notificationsApi.action(id, key)
    } finally {
      void refresh()
    }
  }

  async function dismiss(id: number) {
    patch(id, (n) => ({ ...n, open: false }))
    try {
      await notificationsApi.close(id)
    } catch {
      // Closed already.
    }
    void refresh()
  }

  /** Removes one notification from the list, or all of them. */
  async function clear(id?: number) {
    if (id === undefined) items.value = []
    else patch(id, () => null)
    try {
      await notificationsApi.clear(id === undefined ? undefined : [id])
    } finally {
      void refresh()
    }
  }

  function reset() {
    seq++
    active.value = false
    items.value = []
  }

  return { active, items, toasts, refresh, invoke, dismiss, clear, reset }
})
