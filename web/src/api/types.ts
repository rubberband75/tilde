// Types mirroring the worker's JSON (server/internal/worker). Keep in sync
// with docs/api.md.

export interface SessionInfo {
  user: string
  uid: number
  home: string
}

export interface Entry {
  name: string
  path: string
  type: 'dir' | 'file' | 'other'
  symlink?: boolean
  target?: string
  broken?: boolean
  size: number
  mode: string
  perm: number
  uid: number
  gid: number
  mtime: string
  mime: string
  hidden?: boolean
  /** A thumbnail can be made (GET /api/v1/fs/thumbnail). */
  thumb?: boolean
}

export interface ListResponse {
  path: string
  parent?: string
  entries: Entry[]
}

export interface Place {
  id: string
  name: string
  path: string
  icon: string
}

export interface Mount {
  device: string
  mountPoint: string
  fsType: string
  size: number
  free: number
}

export interface SystemInfo {
  hostname: string
  os: string
  kernel: string
  mounts: Mount[]
}

export interface Hello {
  user: string
  uid: number
  home: string
  shell: string
  hostname: string
  version: string
  protocol: number
}

export interface TrashItem {
  name: string
  originalPath: string
  deletedAt: string
  entry: Entry
}

export interface TrashResult {
  path: string
  name?: string
  error?: string
  code?: string
}

export interface TerminalInfo {
  id: string
  created: string
  cols: number
  rows: number
  exited: boolean
}

// File operation jobs (docs/api.md, D31)

export type JobOp = 'copy' | 'move' | 'drop' | 'delete'
export type JobState = 'preparing' | 'running' | 'waiting' | 'done' | 'failed' | 'cancelled'
export type ConflictAction = 'replace' | 'skip' | 'rename' | 'merge'

export interface Conflict {
  id: number
  source: Entry
  dest: Entry
  suggested: string
  actions: ConflictAction[]
}

export interface JobError {
  path: string
  error: string
  code: string
}

export interface Job {
  id: string
  op: JobOp
  sources: string[]
  dest?: string
  state: JobState
  current?: string
  totalItems: number
  doneItems: number
  totalBytes: number
  doneBytes: number
  conflict?: Conflict
  errors: JobError[]
  errorCount: number
  error?: string
  started: string
  finished?: string
}

export interface Resolution {
  conflict: number
  action: ConflictAction
  name?: string
  applyToAll: boolean
}

/** Frames on the events WebSocket (GET /api/v1/events). */
export type WorkerEvent =
  { type: 'snapshot'; jobs: Job[] } | { type: 'job'; job: Job } | { type: 'job-removed'; id: string }

/** GET /api/v1/fs/properties */
export interface Properties {
  entry: Entry
  owner: string
  group: string
  atime: string
  ctime: string
  /** The user owns it, so may change its permissions. */
  mine: boolean
  free: number
  capacity: number
}

/** Lines of GET /api/v1/fs/usage */
export interface Usage {
  items: number
  size: number
  unreadable: number
  done: boolean
}

/** A sidebar bookmark (~/.config/gtk-3.0/bookmarks, shared with Nemo). */
export interface Bookmark {
  path: string
  name: string
  exists: boolean
}
