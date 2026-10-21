import { api, ApiError, buildUrl, handleResponse, request } from './client'
import type {
  Background,
  BackgroundCollection,
  Bookmark,
  Cell,
  DesktopLayout,
  Entry,
  Hello,
  ListResponse,
  Place,
  Properties,
  SystemInfo,
  TrashItem,
  TrashResult,
  Usage,
} from './types'

export const fsApi = {
  hello: () => api.get<Hello>('/api/v1/hello'),
  system: () => api.get<SystemInfo>('/api/v1/system'),
  places: () => api.get<{ places: Place[] }>('/api/v1/places').then((r) => r.places),
  bookmarks: () => api.get<{ bookmarks: Bookmark[] }>('/api/v1/bookmarks').then((r) => r.bookmarks),
  /** Adds a bookmark, or renames it if the folder is already bookmarked. */
  addBookmark: (path: string, name?: string) =>
    api.post<{ bookmarks: Bookmark[] }>('/api/v1/bookmarks', { path, name }).then((r) => r.bookmarks),
  removeBookmark: (path: string) =>
    request<{ bookmarks: Bookmark[] }>('DELETE', buildUrl('/api/v1/bookmarks', { path })).then((r) => r.bookmarks),
  background: () => api.get<Background>('/api/v1/desktop/background'),
  /** Saves Tilde's own background; returns the one now shown. */
  setBackground: (bg: Omit<Background, 'source'>) => api.put<Background>('/api/v1/desktop/background', bg),
  /** Drops Tilde's background, so the desktop follows Cinnamon's again. */
  resetBackground: () => api.put<Background>('/api/v1/desktop/background', { reset: true }),
  backgroundCollections: () =>
    api.get<{ collections: BackgroundCollection[] }>('/api/v1/desktop/backgrounds').then((r) => r.collections),
  desktopLayout: () => api.get<DesktopLayout>('/api/v1/desktop/layout'),
  /** Merges positions into the desktop layout (null removes one); replace starts from nothing. */
  saveDesktopLayout: (positions: Record<string, Cell | null>, replace = false) =>
    api.post<DesktopLayout>('/api/v1/desktop/layout', { positions, replace }),

  list: (path: string) => api.get<ListResponse>('/api/v1/fs/list', { path }),
  properties: (path: string) => api.get<Properties>('/api/v1/fs/properties', { path }),
  chmod: (path: string, perm: number) => api.post<Entry>('/api/v1/fs/chmod', { path, perm }),
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

  /** URL of a file's thumbnail; mtime in the URL lets the browser cache it until the file changes. */
  thumbnailUrl: (path: string, mtime: string, size: 'normal' | 'large') =>
    buildUrl('/api/v1/fs/thumbnail', { path, size, v: mtime }),

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
 * Reads an NDJSON response line by line. onLine returns true to stop early.
 * A non-2xx response throws the API error.
 */
async function readNdjson<T>(url: string, onLine: (msg: T) => boolean | void, signal?: AbortSignal): Promise<void> {
  const res = await fetch(url, { credentials: 'same-origin', signal, headers: { Accept: 'application/x-ndjson' } })
  if (!res.ok || !res.body) {
    await handleResponse(res, url.split('?')[0]!)
    throw new ApiError(res.status, 'error', res.statusText)
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) return
    buf += value
    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl)
      buf = buf.slice(nl + 1)
      if (line && onLine(JSON.parse(line) as T)) {
        void reader.cancel()
        return
      }
    }
  }
}

/**
 * Reads a stream of entries (a streamed listing or a search, see
 * docs/api.md): onEntries gets each batch as it arrives. Resolves with the
 * final line when the stream is complete.
 */
async function readEntryStream(
  url: string,
  onEntries: (batch: Entry[]) => void,
  signal?: AbortSignal,
): Promise<{ total: number; truncated?: boolean }> {
  let end: { total: number; truncated?: boolean } | undefined
  await readNdjson<StreamLine>(
    url,
    (msg) => {
      if ('entries' in msg) onEntries(msg.entries)
      else if ('error' in msg) throw new ApiError(500, msg.code, msg.error)
      else if ('done' in msg) {
        end = msg
        return true
      }
    },
    signal,
  )
  if (!end) throw new ApiError(0, 'network', 'The response was cut off.')
  return end
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

/**
 * Counts what is inside paths (folder sizes), calling onProgress about four
 * times a second until done. Abort the signal to stop counting.
 */
export function usageStream(paths: string[], onProgress: (u: Usage) => void, signal?: AbortSignal) {
  const q = new URLSearchParams()
  for (const p of paths) q.append('path', p)
  return readNdjson<Usage>(`/api/v1/fs/usage?${q}`, (u) => void onProgress(u), signal)
}
