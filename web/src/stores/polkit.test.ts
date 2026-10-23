import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PolkitRequest } from '@/api/types'
import { usePolkit } from './polkit'

const mount: PolkitRequest = {
  id: 'r1',
  action: 'org.freedesktop.udisks2.filesystem-mount',
  message: 'Authentication is required to mount TILDE-USB',
  identities: ['alice', 'admin'],
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

let requests: PolkitRequest[]
let respond: (body: { id: string; identity?: string; answer: string }) => Response
const bodies: unknown[] = []

beforeEach(() => {
  setActivePinia(createPinia())
  requests = [mount]
  bodies.length = 0
  respond = () => new Response(null, { status: 204 })
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const body: unknown = init?.body ? JSON.parse(init.body as string) : undefined
      if (body) bodies.push(body)
      if (input === '/api/v1/polkit') return Promise.resolve(json(200, { agent: true, requests }))
      if (input === '/api/v1/polkit/respond') return Promise.resolve(respond(body as never))
      if (input === '/api/v1/polkit/cancel') {
        requests = []
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      return Promise.resolve(json(404, { error: 'no', code: 'not_found' }))
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('polkit store', () => {
  it('shows the oldest request and answers with the chosen identity', async () => {
    const p = usePolkit()
    await p.refresh()
    expect(p.current?.id).toBe('r1')
    respond = () => json(200, { prompt: { text: 'Verification code:', echo: false } })
    const prompt = await p.respond('r1', 'secret', 'admin')
    expect(prompt?.text).toBe('Verification code:')
    expect(bodies.at(-1)).toEqual({ id: 'r1', identity: 'admin', answer: 'secret' })
  })

  it('keeps the request after a wrong password, with polkit-agent-helper-1’s reason', async () => {
    const p = usePolkit()
    await p.refresh()
    respond = () => json(401, { error: 'Wrong password', code: 'auth_failed' })
    await expect(p.respond('r1', 'nope')).rejects.toMatchObject({ code: 'auth_failed', message: 'Wrong password' })
    await vi.waitFor(() => expect(p.requests).toHaveLength(1))
  })

  it('treats a request that ended elsewhere as done', async () => {
    const p = usePolkit()
    await p.refresh()
    respond = () => {
      requests = []
      return json(404, { error: 'ended', code: 'not_found' })
    }
    await expect(p.respond('r1', 'secret')).resolves.toBeUndefined()
    await vi.waitFor(() => expect(p.current).toBeUndefined())
  })

  it('closes the dialog at once on cancel', async () => {
    const p = usePolkit()
    await p.refresh()
    const done = p.cancel('r1')
    expect(p.current).toBeUndefined()
    await done
    expect(bodies.at(-1)).toEqual({ id: 'r1' })
  })

  it('forgets everything on logout, even a list still on its way', async () => {
    const p = usePolkit()
    const pending = p.refresh()
    p.reset()
    await pending
    expect(p.requests).toEqual([])
  })
})
