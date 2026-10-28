// Types mirroring the worker's JSON (server/internal/worker). Keep in sync
// with docs/api.md.

/** GET /api/auth/session, and what logging in and unlocking return. */
export interface SessionInfo extends LockTiming {
  user: string
  uid: number
  home: string
  /** This browser shows the lock screen until it is unlocked (D40). */
  locked: boolean
}

/** A question PAM asks beyond the password, such as a verification code (D43). */
export interface AuthPrompt {
  /** PAM's own words, e.g. "Verification code:". */
  text: string
  /** The answer may be shown as it is typed. */
  echo: boolean
  /** PAM's messages since the last question. */
  info?: string[]
}

/** What logging in, unlocking and answering return: done, or another question. */
export type AuthStep = SessionInfo | { prompt: AuthPrompt }

/** How input in the browser keeps it from locking; POST /api/auth/activity. */
export interface LockTiming {
  /** Seconds without input after which this browser locks; 0 never. */
  lockAfter: number
  /** How often, in seconds, to report input while there is some. */
  heartbeat: number
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
  | { type: 'network' }
  /** polkit's authentication requests changed: fetch /api/v1/polkit again (D44). */
  | { type: 'polkit' }

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

/**
 * polkit asking for a password (D44): a program in the session (Mount in
 * Files, pkexec in a terminal) wants something that needs one.
 */
export interface PolkitRequest {
  id: string
  /** polkit's action id, e.g. org.freedesktop.udisks2.filesystem-mount. */
  action: string
  /** polkit's own text, e.g. "Authentication is required to mount TILDE-USB". */
  message: string
  icon?: string
  /** Users whose password polkit accepts; the user first when listed. */
  identities: string[]
  /** A question after the password (a verification code), waiting for its answer. */
  prompt?: AuthPrompt
}

/** GET /api/v1/network (D55): NetworkManager's wired and Wi-Fi devices. */
export interface NetworkState {
  /** False without NetworkManager; the applet is hidden. */
  available: boolean
  wifiEnabled: boolean
  /** The radio's own switch (airplane-mode key); software cannot turn it back on. */
  wifiHardware: boolean
  devices: NetDevice[]
}

export interface NetDevice {
  id: string
  interface: string
  type: 'ethernet' | 'wifi'
  state: 'connected' | 'connecting' | 'disconnected' | 'unavailable' | 'deactivating' | 'failed'
  connection?: string
  /** IP addresses, without prefix lengths. */
  addresses?: string[]
  /** Carries the default route. */
  default: boolean
  networks?: WifiNetwork[]
}

export interface WifiNetwork {
  ssid: string
  /** 0–100. */
  strength: number
  /** none, psk (WPA2 Personal), sae (WPA3 Personal); enterprise and other need a connections editor. */
  security: 'none' | 'psk' | 'sae' | 'enterprise' | 'other'
  active: boolean
  /** NetworkManager has a saved connection for it. */
  known: boolean
}

/** POST /api/v1/network/connect */
export interface ConnectResult {
  connected: boolean
  reason: '' | 'wrong_password' | 'failed' | 'timeout'
}

/** GET /api/v1/polkit */
export interface PolkitState {
  /** Tilde's agent is registered for the session, so polkit can ask. */
  agent: boolean
  requests: PolkitRequest[]
}

/** One of the user's signed-in browsers; GET /api/auth/clients (D47). */
export interface ClientInfo {
  id: string
  /** Where it connects from. */
  address: string
  /** Its User-Agent header: shown, never trusted. */
  agent: string
  /** When it logged in. */
  since: string
  lastSeen: string
  lastInput: string
  locked: boolean
  /** A tab shows the desktop now. */
  connected: boolean
  /** The browser asking. */
  current: boolean
}

/** GET /api/v1/text: a file opened in the text editor (D48). */
export interface TextFile {
  text: string
  /** The contents' version; a save names it, to find changes on disk. */
  version: string
  /** The user may save it (the kernel's answer at the time). */
  writable: boolean
  entry: Entry
}

/** PUT /api/v1/text */
export interface SavedText {
  version: string
  entry: Entry
}

/** The editor's unsaved changes, kept by the worker (D48). */
/** GET /api/v1/drafts without a key (D58): a draft without its text. */
export interface DraftInfo {
  key: string
  base?: string
  updated: string
  /** Bytes of text. */
  size: number
  /** The first line with something on it, at most 80 characters. */
  preview: string
}

export interface Draft {
  key: string
  text: string
  /** The file's version the changes started from. */
  base?: string
  updated: string
}
