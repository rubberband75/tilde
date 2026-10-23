import { describe, expect, it, vi } from 'vitest'
import { ApiError, buildUrl, handleResponse, isSessionLost, onLocked, onSessionExpired } from './client'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('handleResponse', () => {
  it('returns JSON bodies and nothing for 204', async () => {
    await expect(handleResponse(jsonResponse(200, { a: 1 }), '/api/v1/hello')).resolves.toEqual({ a: 1 })
    await expect(handleResponse(new Response(null, { status: 204 }), '/api/v1/x')).resolves.toBeUndefined()
  })

  it('throws ApiError with the server code', async () => {
    const err = await handleResponse(jsonResponse(409, { error: 'exists', code: 'exists' }), '/api/v1/fs/mkdir').catch(
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'exists', message: 'exists' })
  })

  it('reports an ended session only for worker API calls', async () => {
    const seen = vi.fn()
    const off = onSessionExpired(seen)
    const ended = { error: 'gone', code: 'session_ended' }
    await handleResponse(jsonResponse(401, ended), '/api/v1/fs/list').catch(() => {})
    expect(seen).toHaveBeenCalledWith('session_ended')

    seen.mockClear()
    await handleResponse(jsonResponse(401, ended), '/api/auth/session').catch(() => {})
    await handleResponse(jsonResponse(401, { error: 'no', code: 'auth_failed' }), '/api/v1/x').catch(() => {})
    expect(seen).not.toHaveBeenCalled()
    off()
  })
})

describe('locked browsers', () => {
  it('reports a 423 locked answer and counts it as a lost session', async () => {
    const seen = vi.fn()
    const off = onLocked(seen)
    const err = await handleResponse(jsonResponse(423, { error: 'locked', code: 'locked' }), '/api/v1/hello').catch(
      (e: unknown) => e,
    )
    expect(seen).toHaveBeenCalledTimes(1)
    expect(isSessionLost(err)).toBe(true)
    expect(isSessionLost(new ApiError(404, 'not_found', 'x'))).toBe(false)
    off()
  })
})

describe('buildUrl', () => {
  it('drops undefined values and encodes the rest', () => {
    expect(buildUrl('/p')).toBe('/p')
    expect(buildUrl('/p', { path: '/a b', download: undefined })).toBe('/p?path=%2Fa+b')
  })
})
