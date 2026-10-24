import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ClientInfo } from '@/api/types'
import { useClients } from './clients'

const client = (id: string, current = false): ClientInfo => ({
  id,
  address: '192.0.2.1',
  agent: 'Firefox',
  since: '2026-10-23T10:00:00Z',
  lastSeen: '2026-10-23T11:00:00Z',
  lastInput: '2026-10-23T11:00:00Z',
  locked: false,
  connected: true,
  current,
})

let list: ClientInfo[]
const posts: unknown[] = []

beforeEach(() => {
  setActivePinia(createPinia())
  list = [client('me', true), client('other')]
  posts.length = 0
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      if (input === '/api/auth/clients') {
        return Promise.resolve(
          new Response(JSON.stringify({ clients: list }), { headers: { 'content-type': 'application/json' } }),
        )
      }
      posts.push(JSON.parse(init!.body as string))
      list = list.filter((c) => c.id !== 'other')
      return Promise.resolve(new Response(null, { status: 204 }))
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('clients store', () => {
  it('lists the browsers and signs another out', async () => {
    const s = useClients()
    await s.refresh()
    expect(s.others.map((c) => c.id)).toEqual(['other'])
    await s.signOut('other')
    expect(posts).toEqual([{ id: 'other' }])
    expect(s.clients.map((c) => c.id)).toEqual(['me'])
  })

  it('forgets the list when the desktop goes, even one still on its way', async () => {
    const s = useClients()
    const pending = s.refresh()
    s.stop()
    await pending
    expect(s.clients).toEqual([])
  })
})
