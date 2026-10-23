import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError } from '@/api/client'
import { polkitApi } from '@/api/polkit'
import type { PolkitRequest } from '@/api/types'

/**
 * polkit's requests for a password (D44), kept in sync over the events
 * socket: every browser of the user shows the dialog, and the first answer
 * ends it everywhere. The password goes straight to the worker and is
 * never kept here.
 */
export const usePolkit = defineStore('polkit', () => {
  const requests = ref<PolkitRequest[]>([])
  /** The request the dialog shows: the oldest. */
  const current = computed(() => requests.value[0])
  let seq = 0

  async function refresh() {
    const n = ++seq
    try {
      const state = await polkitApi.list()
      if (n === seq) requests.value = state.requests
    } catch {
      // The next event or reconnect brings the list.
    }
  }

  /** See polkitApi.respond. A request that ended meanwhile resolves to undefined. */
  async function respond(id: string, answer: string, identity?: string) {
    try {
      return await polkitApi.respond(id, answer, identity)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') return undefined
      throw e
    } finally {
      void refresh()
    }
  }

  async function cancel(id: string) {
    requests.value = requests.value.filter((r) => r.id !== id)
    try {
      await polkitApi.cancel(id)
    } catch {
      // Ended already.
    }
    void refresh()
  }

  function reset() {
    seq++
    requests.value = []
  }

  return { requests, current, refresh, respond, cancel, reset }
})
