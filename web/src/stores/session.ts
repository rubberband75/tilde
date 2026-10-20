import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, ApiError, onSessionExpired } from '@/api/client'
import type { SessionInfo } from '@/api/types'

export const useSession = defineStore('session', () => {
  const info = ref<SessionInfo | null>(null)
  const status = ref<'checking' | 'loggedOut' | 'loggedIn'>('checking')
  /** Shown on the login screen, e.g. why the user was sent back to it. */
  const notice = ref('')

  async function check() {
    try {
      info.value = await api.get<SessionInfo>('/api/auth/session')
      status.value = 'loggedIn'
    } catch (e) {
      if (e instanceof ApiError && e.code === 'session_ended') notice.value = 'Your session has ended.'
      info.value = null
      status.value = 'loggedOut'
    }
  }

  async function login(username: string, password: string) {
    info.value = await api.post<SessionInfo>('/api/auth/login', { username, password })
    notice.value = ''
    status.value = 'loggedIn'
  }

  /** Log out ends the whole session, like Mint's Log Out (decision 4d). */
  async function logout() {
    try {
      await api.post('/api/auth/logout')
    } finally {
      info.value = null
      status.value = 'loggedOut'
    }
  }

  onSessionExpired((code) => {
    if (status.value !== 'loggedIn') return
    info.value = null
    status.value = 'loggedOut'
    notice.value = code === 'session_ended' ? 'Your session has ended.' : 'You have been logged out.'
  })

  return { info, status, notice, check, login, logout }
})
