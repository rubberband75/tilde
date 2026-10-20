// Thin fetch wrapper for Tilde's API.
//
// /api/auth/* is handled by the main server; /api/v1/* is proxied to the
// user's worker. A 401 with code "session_ended" or "not_logged_in" from
// /api/v1/* means the session is gone; listeners (the session store) send
// the user back to the login screen instead of retrying forever.

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
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
  const headers: Record<string, string> = { Accept: 'application/json' }
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
    if (res.status === 401 && path.startsWith('/api/v1/') && (code === 'session_ended' || code === 'not_logged_in')) {
      reportExpired(code)
    }
    throw new ApiError(res.status, code, err.error ?? res.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(path: string, query?: Query) => request<T>('GET', path, { query }),
  post: <T>(path: string, json?: unknown) => request<T>('POST', path, { json }),
  del: <T>(path: string) => request<T>('DELETE', path),
}

/** Absolute ws:// or wss:// URL for a same-origin path. */
export function wsUrl(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}${path}`
}
