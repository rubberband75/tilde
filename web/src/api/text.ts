import { api, buildUrl, handleResponse, request } from './client'
import type { Draft, DraftInfo, SavedText, TextFile } from './types'

export const textApi = {
  get: (path: string) => api.get<TextFile>('/api/v1/text', { path }),
  /**
   * Saves text as path. base is the version it was opened at ('' for a new
   * file); without force a file that changed since answers 409 `changed`,
   * and an existing one for a new file 409 `exists`.
   */
  save: async (path: string, text: string, base: string, force = false) => {
    const res = await fetch(buildUrl('/api/v1/text', { path, base, force: force ? 1 : undefined }), {
      method: 'PUT',
      headers: { Accept: 'application/json', 'Content-Type': 'text/plain; charset=utf-8' },
      body: text,
      credentials: 'same-origin',
    })
    return handleResponse<SavedText>(res, '/api/v1/text')
  },
}

export const draftsApi = {
  get: (key: string) => api.get<Draft>('/api/v1/drafts', { key }),
  /** Every draft, newest first, without its text. */
  list: () => api.get<{ drafts: DraftInfo[] }>('/api/v1/drafts').then((r) => r.drafts),
  put: (key: string, text: string, base: string) =>
    request<void>('PUT', buildUrl('/api/v1/drafts', { key }), { json: { text, base } }),
  remove: (key: string) => request<void>('DELETE', buildUrl('/api/v1/drafts', { key })),
}
