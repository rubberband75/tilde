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

/** GET /api/v1/drives: a volume, mounted or not (D39). */
export interface Volume {
  /** udisks block object path, or "mount:" + mount point for mounts udisks does not know. */
  id: string
  device: string
  name: string
  fsType: string
  size: number
  free?: number
  /** Empty while not mounted. */
  mountPoint?: string
  removable: boolean
  canEject: boolean
  system: boolean
  /** freedesktop icon names, most specific first. */
  icons: string[]
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
  /** The name in the home trash, or the absolute path inside a drive's trash. */
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
  | { type: 'snapshot'; jobs: Job[] }
  | { type: 'job'; job: Job }
  | { type: 'job-removed'; id: string }
  /** Watched folders whose contents changed. */
  | { type: 'fs'; dirs: string[] }
  /** Drives or volumes changed (plugged in, mounted, unmounted): fetch /api/v1/drives again. */
  | { type: 'drives' }

/** Client → worker on the events WebSocket: the folders to watch (replaces the previous set). */
export type ClientMessage = { type: 'watch'; dirs: string[] }

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

/** A desktop grid position, from the top left. */
export interface Cell {
  col: number
  row: number
}

/** GET /api/v1/desktop/layout. Keys are file names in the Desktop folder, or computer:///, home:///, trash:///. */
export interface DesktopLayout {
  /** False until icons are arranged in Tilde; then nemo is absent. */
  saved: boolean
  positions: Record<string, Cell>
  /** Nemo's arrangement in screen pixels, to start from; grid is its cell size. */
  nemo?: { grid?: [number, number]; icons: Record<string, { x: number; y: number }> }
}

/** GET /api/v1/desktop/background, in Cinnamon's terms (org.cinnamon.desktop.background). */
export interface Background {
  /** tilde: picked in Tilde; cinnamon: the user's Cinnamon background; default: neither (theme background). */
  source: 'tilde' | 'cinnamon' | 'default'
  /** Absolute path; empty for colors only. */
  picture: string
  options: BackgroundOptions
  primary: string
  secondary: string
  shading: 'solid' | 'vertical' | 'horizontal'
}

export type BackgroundOptions = 'none' | 'wallpaper' | 'centered' | 'scaled' | 'stretched' | 'zoom' | 'spanned'

/** GET /api/v1/desktop/backgrounds */
export interface BackgroundCollection {
  name: string
  images: Entry[]
}
