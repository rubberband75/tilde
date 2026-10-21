import { api, ApiError, buildUrl, handleResponse } from './client'
import type { Entry, Hello, ListResponse, Place, SystemInfo, TrashItem, TrashResult } from './types'

export const fsApi = {
  hello: () => api.get<Hello>('/api/v1/hello'),
  system: () => api.get<SystemInfo>('/api/v1/system'),
  places: () => api.get<{ places: Place[] }>('/api/v1/places').then((r) => r.places),

  list: (path: string) => api.get<ListResponse>('/api/v1/fs/list', { path }),
  listStream: (path: string, onEntries: (batch: Entry[]) => void, signal?: AbortSignal) =>
    listStream(path, onEntries, signal),
  stat: (path: string) => api.get<Entry>('/api/v1/fs/stat', { path }),
  mkdir: (path: string) => api.post<Entry>('/api/v1/fs/mkdir', { path }),
  rename: (from: string, to: string) => api.post<Entry>('/api/v1/fs/rename', { from, to }),
  trash: (paths: string[]) =>
    api.post<{ results: TrashResult[] }>('/api/v1/fs/trash', { paths }).then((r) => r.results),

  trashList: () => api.get<{ dir: string; items: TrashItem[] }>('/api/v1/trash'),
  restore: (names: string[]) => api.post<{ restored: string[] }>('/api/v1/trash/restore', { names }),
  emptyTrash: () => api.post<void>('/api/v1/trash/empty'),

  /** URL that streams a file. With download, the browser saves it. */
  contentUrl: (path: string, download = false) =>
    buildUrl('/api/v1/fs/content', { path, download: download ? '1' : undefined }),

  /**
   * Uploads a file. Uses XMLHttpRequest because fetch cannot report upload
   * progress.
   */
  upload(
    path: string,
    file: Blob,
    onProgress?: (loaded: number, total: number) => void,
    overwrite = false,
  ): Promise<Entry> {
    const url = buildUrl('/api/v1/fs/content', { path, overwrite: overwrite ? '1' : undefined })
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest()
      xhr.open('PUT', url)
      xhr.withCredentials = true
      xhr.upload.onprogress = (e) => onProgress?.(e.loaded, e.total)
      xhr.onerror = () => reject(new ApiError(0, 'network', 'Network error during upload'))
      xhr.onload = () => {
        const res = new Response(xhr.responseText || null, {
          status: xhr.status,
          headers: { 'content-type': xhr.getResponseHeader('content-type') ?? '' },
        })
        handleResponse<Entry>(res, '/api/v1/fs/content').then(resolve, reject)
      }
      xhr.send(file)
    })
  },
}

export function joinPath(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir}/${name}`
}

export function baseName(path: string): string {
  if (path === '/') return '/'
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(i + 1) : path
}

export function dirName(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

/** Picks "Untitled Folder", "Untitled Folder 2", ... not present in names. */
export function uniqueName(base: string, names: Set<string>): string {
  if (!names.has(base)) return base
  for (let i = 2; ; i++) {
    const candidate = `${base} ${i}`
    if (!names.has(candidate)) return candidate
  }
}

type StreamLine =
  | { path: string; parent?: string }
  | { entries: Entry[] }
  | { done: true; total: number; truncated?: boolean }
  | { error: string; code: string }

/**
 * Reads an NDJSON response of entries (a streamed listing or a search, see
 * docs/api.md): onEntries gets each batch as it arrives. Resolves with the
 * final line when the stream is complete.
 */
async function readEntryStream(
  url: string,
  onEntries: (batch: Entry[]) => void,
  signal?: AbortSignal,
): Promise<{ total: number; truncated?: boolean }> {
  const res = await fetch(url, { credentials: 'same-origin', signal, headers: { Accept: 'application/x-ndjson' } })
  const path = url.split('?')[0]!
  if (!res.ok || !res.body) {
    await handleResponse(res, path)
    throw new ApiError(res.status, 'error', res.statusText)
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += value
    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl)
      buf = buf.slice(nl + 1)
      if (!line) continue
      const msg = JSON.parse(line) as StreamLine
      if ('entries' in msg) onEntries(msg.entries)
      else if ('error' in msg) throw new ApiError(500, msg.code, msg.error)
      else if ('done' in msg) return msg
    }
  }
  throw new ApiError(0, 'network', 'The response was cut off.')
}

/** Streams a folder listing in unsorted batches. */
export function listStream(path: string, onEntries: (batch: Entry[]) => void, signal?: AbortSignal) {
  return readEntryStream(buildUrl('/api/v1/fs/list', { path, stream: 1 }), onEntries, signal)
}

/** Searches below path for names containing every word of q. */
export function searchStream(
  path: string,
  q: string,
  hidden: boolean,
  onEntries: (batch: Entry[]) => void,
  signal?: AbortSignal,
) {
  return readEntryStream(buildUrl('/api/v1/fs/search', { path, q, hidden: hidden ? 1 : undefined }), onEntries, signal)
}
