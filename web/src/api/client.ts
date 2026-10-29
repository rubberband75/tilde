// Thin fetch wrapper for Tilde's API.
//
// /api/auth/* is handled by the main server; /api/v1/* is proxied to the
// user's worker. A 401 with code "session_ended" or "not_logged_in" from
// /api/v1/* means the session is gone; listeners (the session store) send
// the user back to the login screen instead of retrying forever. A 423
// "locked" means this browser was locked (idle, or Lock Screen on any
// device, D40); listeners show the lock screen.

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

/**
 * Whose desktop this tab is (D76). Requests say so, and the server refuses
 * one whose cookie is another user's: a page that swapped Tilde's cookie
 * for another session's (cookies are per host, so a local web app's page
 * could try) gets nothing through, and the tab goes back to the login.
 */
let sessionUser = ''

export function setSessionUser(user: string) {
  sessionUser = user
}

/** The header that says whose desktop this is, once logged in. */
export function userHeaders(): Record<string, string> {
  return sessionUser ? { 'X-Tilde-User': sessionUser } : {}
}

type ExpiredListener = (code: string) => void
const expiredListeners = new Set<ExpiredListener>()

export function onSessionExpired(fn: ExpiredListener): () => void {
  expiredListeners.add(fn)
  return () => expiredListeners.delete(fn)
}

export function reportExpired(code: string): void {
  expiredListeners.forEach((fn) => fn(code))
}

const lockedListeners = new Set<() => void>()

export function onLocked(fn: () => void): () => void {
  lockedListeners.add(fn)
  return () => lockedListeners.delete(fn)
}

/** True for errors that mean this browser has no usable session: ended, logged out or locked. */
export function isSessionLost(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 401 || e.status === 423)
}

export type Query = Record<string, string | number | boolean | undefined>

interface RequestOptions {
  query?: Query
  json?: unknown
  signal?: AbortSignal
}

export function buildUrl(path: string, query?: Query): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...userHeaders() }
  let body: BodyInit | undefined
  if (opts.json !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.json)
  }
  const res = await fetch(buildUrl(path, opts.query), {
    method,
    headers,
    body,
    credentials: 'same-origin',
    signal: opts.signal,
  })
  return handleResponse<T>(res, path)
}

export async function handleResponse<T>(res: Response, path: string): Promise<T> {
  if (res.status === 204) return undefined as T
  const isJSON = (res.headers.get('content-type') ?? '').includes('application/json')
  const data: unknown = isJSON ? await res.json() : await res.text()
  if (!res.ok) {
    const err = (isJSON ? data : {}) as { error?: string; code?: string }
    const code = err.code ?? 'error'
    if (
      res.status === 401 &&
      path.startsWith('/api/v1/') &&
      (code === 'session_ended' || code === 'not_logged_in' || code === 'session_changed')
    ) {
      reportExpired(code)
    }
    if (res.status === 423 && code === 'locked') lockedListeners.forEach((fn) => fn())
    throw new ApiError(res.status, code, err.error ?? res.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(path: string, query?: Query) => request<T>('GET', path, { query }),
  post: <T>(path: string, json?: unknown) => request<T>('POST', path, { json }),
  put: <T>(path: string, json?: unknown) => request<T>('PUT', path, { json }),
  del: <T>(path: string) => request<T>('DELETE', path),
}

/** Absolute ws:// or wss:// URL for a same-origin path. */
export function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  // A WebSocket cannot carry headers: the user goes in the query.
  const user = sessionUser ? `${path.includes('?') ? '&' : '?'}tilde-user=${encodeURIComponent(sessionUser)}` : ''
  return `${proto}//${location.host}${path}${user}`
}
