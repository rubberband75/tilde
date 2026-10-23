import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import type { SessionInfo } from '@/api/types'
import { useSession } from './session'

const alice: SessionInfo = {
  user: 'alice',
  uid: 1000,
  home: '/home/alice',
  locked: false,
  lockAfter: 900,
  heartbeat: 15,
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

type Route = (method: string) => Response | Promise<Response>
let routes: Record<string, Route>
const calls: string[] = []

beforeEach(() => {
  setActivePinia(createPinia())
  routes = {}
  calls.length = 0
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${input}`)
      const route = routes[input]
      return Promise.resolve(route ? route(method) : json(404, { error: 'no', code: 'not_found' }))
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('session store and the lock screen', () => {
  it('starts on the lock screen when the server says this browser is locked', async () => {
    routes['/api/auth/session'] = () => json(200, { ...alice, locked: true })
    const s = useSession()
    await s.check()
    expect(s.status).toBe('locked')
    expect(s.info?.user).toBe('alice')
  })

  it('locks when a worker request answers 423', async () => {
    routes['/api/auth/session'] = () => json(200, alice)
    routes['/api/v1/hello'] = () => json(423, { error: 'this screen is locked', code: 'locked' })
    const s = useSession()
    await s.check()
    await api.get('/api/v1/hello').catch(() => {})
    expect(s.status).toBe('locked')
  })

  it('stays on the lock screen after a wrong password, unlocks with the right one', async () => {
    routes['/api/auth/session'] = () => json(200, { ...alice, locked: true })
    let right = false
    routes['/api/auth/unlock'] = () =>
      right ? json(200, alice) : json(401, { error: 'Incorrect password.', code: 'auth_failed' })
    const s = useSession()
    await s.check()
    await expect(s.unlock('guess')).rejects.toMatchObject({ code: 'auth_failed' })
    expect(s.status).toBe('locked')
    right = true
    await s.unlock('secret')
    expect(s.status).toBe('loggedIn')
  })

  it('goes back to login after too many wrong passwords', async () => {
    routes['/api/auth/session'] = () => json(200, { ...alice, locked: true })
    routes['/api/auth/unlock'] = () =>
      json(401, { error: 'Too many wrong passwords. Log in again.', code: 'too_many_attempts' })
    const s = useSession()
    await s.check()
    await s.unlock('guess')
    expect(s.status).toBe('loggedOut')
    expect(s.notice).toBe('Too many wrong passwords. Log in again.')
  })

  it('keeps the desktop if the server could not lock', async () => {
    routes['/api/auth/session'] = () => json(200, alice)
    routes['/api/auth/lock'] = () => Promise.reject(new TypeError('network down'))
    const s = useSession()
    await s.check()
    await expect(s.lock()).rejects.toThrow()
    expect(s.status).toBe('loggedIn')
  })

  it('picks up an unlock from another tab of the browser', async () => {
    let locked = true
    routes['/api/auth/session'] = () => json(200, { ...alice, locked })
    const s = useSession()
    await s.check()
    locked = false
    await s.refresh()
    expect(s.status).toBe('loggedIn')
  })

  it('switching user only signs this browser out', async () => {
    routes['/api/auth/session'] = () => json(200, { ...alice, locked: true })
    routes['/api/auth/leave'] = () => new Response(null, { status: 204 })
    const s = useSession()
    await s.check()
    await s.leave()
    expect(s.status).toBe('loggedOut')
    expect(calls).toContain('POST /api/auth/leave')
    expect(calls).not.toContain('POST /api/auth/logout')
  })
})
