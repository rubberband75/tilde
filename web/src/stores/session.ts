import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, ApiError, onLocked, onSessionExpired } from '@/api/client'
import type { AuthPrompt, AuthStep, SessionInfo } from '@/api/types'

export const useSession = defineStore('session', () => {
  const info = ref<SessionInfo | null>(null)
  /**
   * locked: the server has locked this browser (D40). The desktop is
   * dropped from the page, not covered, until it is unlocked.
   */
  const status = ref<'checking' | 'loggedOut' | 'locked' | 'loggedIn'>('checking')
  /** Shown on the login screen, e.g. why the user was sent back to it. */
  const notice = ref('')
  /**
   * PAM's question waiting for an answer during a login or unlock, such
   * as a verification code (D43); the login and lock screens ask it.
   */
  const prompt = ref<AuthPrompt | null>(null)

  function show(s: SessionInfo) {
    info.value = s
    status.value = s.locked ? 'locked' : 'loggedIn'
  }

  /** Takes a login, unlock or answer response: done, or another question. */
  function step(res: AuthStep) {
    if ('prompt' in res) {
      prompt.value = res.prompt
      return
    }
    prompt.value = null
    show(res)
  }

  function end(code: string, message = '') {
    info.value = null
    prompt.value = null
    status.value = 'loggedOut'
    notice.value = message || (code === 'session_ended' ? 'Your session has ended.' : 'You have been logged out.')
  }

  function ended(e: unknown): boolean {
    return e instanceof ApiError && (e.code === 'session_ended' || e.code === 'not_logged_in')
  }

  async function check() {
    try {
      show(await api.get<SessionInfo>('/api/auth/session'))
    } catch (e) {
      if (e instanceof ApiError && e.code === 'session_ended') notice.value = 'Your session has ended.'
      info.value = null
      status.value = 'loggedOut'
    }
  }

  /**
   * Catches up with what happened while the tab was hidden: a lock, an
   * unlock in another tab of this browser, or the end of the session.
   */
  async function refresh() {
    if (status.value !== 'loggedIn' && status.value !== 'locked') return
    try {
      show(await api.get<SessionInfo>('/api/auth/session'))
    } catch (e) {
      if (ended(e)) end((e as ApiError).code)
    }
  }

  async function login(username: string, password: string) {
    step(await api.post<AuthStep>('/api/auth/login', { username, password }))
    notice.value = ''
  }

  /** Answers PAM's question. A wrong answer throws and starts over. */
  async function answer(text: string) {
    try {
      step(await api.post<AuthStep>('/api/auth/answer', { answer: text }))
    } catch (e) {
      prompt.value = null
      if (ended(e)) end((e as ApiError).code)
      else throw e
    }
  }

  /** Gives up on the question and goes back to the password. */
  async function cancelPrompt() {
    prompt.value = null
    await api.post('/api/auth/answer', { cancel: true }).catch(() => {})
  }

  /** Log out ends the whole session, like Mint's Log Out (D15). */
  async function logout() {
    try {
      await api.post('/api/auth/logout')
    } finally {
      info.value = null
      status.value = 'loggedOut'
    }
  }

  /** Locks every browser logged in as this user. Throws if the server could not lock. */
  async function lock() {
    try {
      await api.post('/api/auth/lock')
    } catch (e) {
      if (ended(e)) end((e as ApiError).code)
      throw e
    }
    if (status.value === 'loggedIn') status.value = 'locked'
  }

  /**
   * Unlocks this browser; others stay locked. Throws on a wrong password.
   * After too many, the server signs the browser out: back to login.
   */
  async function unlock(password: string) {
    try {
      step(await api.post<AuthStep>('/api/auth/unlock', { password }))
    } catch (e) {
      if (ended(e)) end((e as ApiError).code)
      else if (e instanceof ApiError && e.code === 'too_many_attempts') end(e.code, e.message)
      else throw e
    }
  }

  /** Signs this browser out from the lock screen; the session keeps running. */
  async function leave() {
    try {
      await api.post('/api/auth/leave')
    } finally {
      info.value = null
      notice.value = ''
      status.value = 'loggedOut'
    }
  }

  onSessionExpired((code) => {
    if (status.value === 'loggedIn' || status.value === 'locked') end(code)
  })

  onLocked(() => {
    if (status.value === 'loggedIn') status.value = 'locked'
  })

  return { info, status, notice, prompt, check, refresh, login, answer, cancelPrompt, logout, lock, unlock, leave }
})
