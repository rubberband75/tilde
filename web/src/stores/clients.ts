import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { ClientInfo } from '@/api/types'

/** How often the panel asks again; opening the list asks at once. */
export const clientsPollMs = 30_000

/**
 * The user's signed-in browsers, for the panel's applet (D47). The server
 * keeps them (one per session cookie); this browser polls while the
 * desktop shows.
 */
export const useClients = defineStore('clients', () => {
  const clients = ref<ClientInfo[]>([])
  const others = computed(() => clients.value.filter((c) => !c.current))
  let timer: ReturnType<typeof setInterval> | undefined
  let seq = 0

  async function refresh() {
    const n = ++seq
    try {
      const res = await api.get<{ clients: ClientInfo[] }>('/api/auth/clients')
      if (n === seq) clients.value = res.clients
    } catch {
      // Locked or signed out: the session store handles that.
    }
  }

  function start() {
    stop()
    void refresh()
    timer = setInterval(() => void refresh(), clientsPollMs)
  }

  function stop() {
    clearInterval(timer)
    timer = undefined
    seq++
    clients.value = []
  }

  /** Signs out another browser of the user. */
  async function signOut(id: string) {
    await api.post('/api/auth/clients/signout', { id })
    clients.value = clients.value.filter((c) => c.id !== id)
    void refresh()
  }

  return { clients, others, refresh, start, stop, signOut }
})
