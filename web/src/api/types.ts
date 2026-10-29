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
  /** Notifications changed: fetch /api/v1/notifications again (D70). */
  | { type: 'notifications' }
  /** The sound server's outputs or streams changed: fetch /api/v1/sound again (D79). */
  | { type: 'sound' }
  /** The settings were saved, in this browser or another (D72): fetch them again. */
  | { type: 'settings' }
  /** While asked for (D61): the latest whole snapshot, every second. */
  | { type: 'resources'; resources: Resources }
  /** Right after asking: the last minute's snapshots, oldest first. */
  | { type: 'resources-history'; history?: Resources[] }

/** CPU, memory, network and disk use (D61), from /proc. */
export interface Resources {
  /** Unix milliseconds. */
  time: number
  /** Percent of all CPUs busy since the previous snapshot, and per CPU. */
  cpu: number
  cores: number[]
  /** Bytes; used is total minus available. */
  memory: { total: number; used: number; cache?: number }
  swap: { total: number; used: number }
  /** Bytes a second, and network totals since boot. */
  netReceive: number
  netSend: number
  netReceived: number
  netSent: number
  diskRead: number
  diskWrite: number
}

/** Client → worker on the events WebSocket: the folders to watch (replaces the previous set). */
export type ClientMessage = { type: 'watch'; dirs: string[] } | { type: 'resources'; on: boolean }

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
/** GET/PUT /api/v1/settings (D72): Tilde's own settings, in ~/.config/tilde. */
export interface Settings {
  lock: {
    /** Tilde's own delay below, rather than the desktop's. */
    own: boolean
    enabled: boolean
    /** 1 to 1440. */
    minutes: number
  }
  appearance: Appearance
  effects: { animations: boolean }
  panel: { resources: boolean; network: boolean; sound: boolean; notifications: boolean; fullscreen: boolean }
}

export type ThemeMode = 'dark' | 'light' | 'mixed'
export type ThemeAccent = 'green' | 'aqua' | 'blue' | 'grey' | 'orange' | 'pink' | 'purple' | 'red' | 'sand' | 'teal'

/** The look (D77): each "desktop" to follow the desktop at the machine. */
export interface Appearance {
  mode: ThemeMode | 'desktop'
  accent: ThemeAccent | 'desktop'
}

/** What the desktop says about the look (Cinnamon's themes, GNOME's scheme). */
export interface DesktopTheme {
  source: 'cinnamon' | 'gnome' | 'default'
  mode: ThemeMode
  accent: ThemeAccent
}

/** When idle browsers lock, and where that came from (D40). */
export interface LockSettings {
  source: 'tilde' | 'cinnamon' | 'gnome' | 'default'
  /** Seconds without input; 0 is never. */
  lockAfter: number
}

/** GET /api/auth/logins (D72): one of the user's recent logins or failed attempts. */
export interface LoginRecord {
  /** Unix milliseconds. */
  time: number
  event: 'login' | 'login_failed' | 'unlock_failed' | 'rate_limited'
  rhost?: string
  text: string
  failed: boolean
}

/** GET /api/v1/processes (D59): one process from /proc. */
export interface Process {
  pid: number
  ppid: number
  /** The kernel's name (at most 15 bytes). */
  name: string
  /** The command line; missing for kernel threads and when unreadable. */
  command?: string
  uid: number
  user: string
  /** The kernel's state letter: R, S, D, Z, T, I, ... */
  state: string
  /** Percent of all CPUs since the previous list. */
  cpu: number
  /** Resident memory, in bytes. */
  memory: number
  nice: number
  threads: number
  /** Start time in clock ticks after boot: with the PID, names this process. */
  start: number
  kernel?: boolean
}

export interface ProcessList {
  processes: Process[]
  /** The user's own uid. */
  uid: number
  /** The worker's PID: ending it ends the session. */
  self: number
  cpus: number
}

export type ProcessSignal = 'TERM' | 'KILL' | 'STOP' | 'CONT' | 'HUP' | 'INT'

/** GET /api/v1/services (D60): a systemd service. */
export interface Service {
  name: string
  description?: string
  /** loaded, not-found, masked...; missing for a unit file not loaded now. */
  load?: string
  /** active, inactive, failed, activating, deactivating, reloading */
  active: string
  /** running, exited, dead, failed, start... */
  sub: string
  /** The unit file's state: enabled, disabled, static, masked...; missing without one. */
  boot?: string
}

/** GET /api/v1/services/journal: one of a service's journal lines. */
export interface JournalEntry {
  /** Unix milliseconds. */
  time: number
  /** syslog's: 0 emergency … 3 error, 4 warning … 7 debug. */
  priority: number
  ident?: string
  pid?: number
  message: string
}

export type ServiceScope = 'system' | 'user'
export type ServiceAction = 'start' | 'stop' | 'restart'

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

/** A program's notification (D70), from the user's session bus. */
export interface Notification {
  id: number
  /** The program's name, as it gave it. */
  app: string
  /** An icon name; or `iconPath`, a picture file; or `image`, a picture sent with it. */
  icon?: string
  iconPath?: string
  image?: boolean
  summary: string
  /** Plain text: markup is removed by the worker. */
  body?: string
  /** Buttons; the "default" one is a click on the notification itself. */
  actions?: { key: string; label: string }[]
  /** 0 low, 1 normal, 2 critical. */
  urgency: number
  /** When it came, in Unix milliseconds. */
  time: number
  /** Shown as a toast; closed ones stay in the list. */
  open: boolean
}

/** GET /api/v1/notifications */
export interface NotificationsState {
  /** Programs' notifications come to Tilde: no other program shows them. */
  active: boolean
  /** Newest first. */
  notifications: Notification[]
}

/** A TCP port one of the user's programs listens on (D76): GET /api/v1/apps. */
export interface LocalApp {
  port: number
  /** 127.0.0.1, ::1, 0.0.0.0, :: or another address. */
  address: string
  process?: string
  pid?: number
  /** It answered an HTTP request; `title` is its page's. */
  http: boolean
  title?: string
}

/** A host app from its .desktop file (D78), run in the user's Xpra session. */
export interface HostApp {
  /** The desktop file ID: "org.gnome.baobab". */
  id: string
  name: string
  comment?: string
  /** An icon name, and the file found for it in the host's icon themes. */
  icon?: string
  iconPath?: string
  /** The menu's category: Tilde's, or Programming or Other. */
  category: string
}

/** A sound output (a sink): speakers, headphones, HDMI (D79). */
export interface SoundOutput {
  name: string
  description: string
  /** Percent, 0 to 150. */
  volume: number
  muted: boolean
  default: boolean
}

/** An app playing sound (a stream). */
export interface SoundApp {
  id: number
  name: string
  icon?: string
  volume: number
  muted: boolean
}

/** GET /api/v1/sound */
export interface SoundState {
  /** A sound server answers (PipeWire or PulseAudio). */
  available: boolean
  outputs: SoundOutput[]
  apps: SoundApp[]
}
