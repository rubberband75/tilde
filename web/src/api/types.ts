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
