# Architecture

Tilde is one Go binary with three roles, plus a Vue frontend embedded in it.

```
 Browser (Vue)                     Host
 ┌──────────────┐   HTTPS + WS   ┌──────────────────────┐
 │ desktop, wm, │ ─────────────▶ │ tilde serve   (root) │  TLS, login, tokens,
 │ apps         │                │                      │  Origin checks, proxy
 └──────────────┘                └──────────┬───────────┘
                                            │ on login: spawn, pipe on stdin/stdout
                                 ┌──────────▼───────────┐
                                 │ tilde session-helper │  PAM authenticate,
                                 │ (root, one per login)│  open logind session
                                 └──────────┬───────────┘
                                            │ exec with user's UID/GID/groups,
                                            │ same pipe
                                 ┌──────────▼───────────┐
                                 │ tilde worker  (user) │  HTTP over yamux:
                                 │                      │  files, jobs, events,
                                 │                      │  trash, thumbnails, PTYs
                                 └──────────┬───────────┘
                                            ▼
                                 Linux: kernel permissions, logind, D-Bus
```

The rule that shapes everything: **the server never touches user data**. It
authenticates and forwards. Everything that reads a file or runs a command
happens in the worker, which runs as the user, so the kernel makes every
permission decision. A bug in the file manager can only do what that user
could already do.

## Processes

### `tilde serve` (root)

- Listens on HTTPS (self-signed certificate by default) or, with `--dev`,
  plain HTTP on loopback for the Vite dev server.
- Serves the embedded frontend (`internal/webui`).
- `/api/auth/*`: login, logout, session, and the lock screen (activity,
  lock, unlock, leave). Login and unlock spawn a session helper.
- `/api/v1/*`: checks the session cookie, then reverse-proxies the request to
  that user's worker over a new yamux stream (`internal/server/sessions.go`).
  WebSocket upgrades pass through, which is how terminals work. A locked
  token gets `423`; locking ends the requests made with it (D40).
- Keeps browser tokens in memory (`tokens.go`) and rate-limits failed logins
  per address (`ratelimit.go`). Per-account lockout is pam_faillock's job.
- Must run as root (to start helpers), except in `--dev` single-user mode.

### `tilde session-helper` (root, one per login)

- Calls PAM from the process's main thread (`cmd/tilde` locks it in `init`):
  pam_loginuid writes `/proc/self/loginuid`, which fails from other threads
  (D32).
- Reads one JSON request (user, password, mode) from stdin, byte by byte so it
  never consumes bytes meant for the worker.
- Runs the PAM `auth` and `account` stacks for service `tilde`. The first
  hidden prompt gets the password; any further question (a verification
  code, D43) goes to the server as a prompt, and the helper waits for the
  answer, still on its main thread.
- In `verify` mode (unlocking), replies and exits.
- In `session` mode, once authenticated, asks the server whether to start:
  the user may already have a session, which this login then joins. To
  start one it sets `XDG_SESSION_TYPE=web`, `XDG_SESSION_CLASS=user`,
  calls `pam_setcred` and `pam_open_session`. `pam_systemd` registers a logind
  session for the helper process and moves it into a session scope, which
  provides `XDG_RUNTIME_DIR` and the session D-Bus bus.
- Starts `tilde worker` with the user's UID, GID and supplementary groups,
  handing it the same stdin/stdout, then waits. When the worker exits it closes
  the PAM session. `Pdeathsig` stops the worker if the helper dies.

### `tilde worker` (the user)

- Writes a `WorkerHello` line, then serves plain HTTP on a yamux session over
  stdin/stdout (`internal/worker`). It never opens a network port.
- Owns the user's terminals (`internal/pty`): PTYs with a 1 MiB ring buffer.
  Terminals survive browser disconnects; reattaching replays the buffer, then
  a forced resize makes full-screen programs redraw.
- Runs file-operation jobs (`jobs.go`, D31): copy, move and delete trees in
  the background with progress, cancel and conflict prompts. Moves use
  `renameat2(RENAME_NOREPLACE)`; copies go through a temporary file; deletes
  walk directory descriptors so a swapped-in symlink is never followed.
- Pushes events on a WebSocket (`events.go`): a snapshot of all jobs on
  connect, then every change as a whole job object, and changes to the
  folders clients show (`watch.go`: one inotify watch per folder for all
  clients, changes coalesced for 200 ms; D34), and drive changes from udisks
  signals (`drives.go`, D39).
- Lists, mounts, unmounts and ejects drives through udisks2 on the system
  bus, as the user (`drives.go`).
- Is the polkit authentication agent of Tilde's web session (`polkit.go`,
  D44): polkit's requests for a password go to a dialog in every browser,
  and polkit's setuid `polkit-agent-helper-1` checks the answers.
- Reads the user's screensaver settings with gsettings and streams the idle
  lock delay to the server (`lock.go`, `/internal/session`, D40), and
  follows its logind session (`logind.go`, D41): sets `LockedHint` when the
  server says every browser is locked, and passes on logind's lock
  requests. These `/internal/` routes are the server's own and never
  proxied (`server/worker_session.go`). The same stream carries the
  worker's audit events (`audit.go`), which the server writes to the
  journal (`server/audit.go`, D46).
- Reads and saves text files for the editor, keeping symlinks,
  permissions and hard links, and keeps its drafts in
  `$XDG_STATE_HOME/tilde/drafts` (`text.go`, `drafts.go`, D48).
- Streams big listings and searches as NDJSON, sniffs MIME types of
  extensionless files (budgeted per listing), and makes thumbnails into the
  shared freedesktop cache with the host's thumbnailers (D33).
- Exits when its pipe closes (logout, server shutdown, session end),
  cancelling jobs and hanging up all terminals first.

## Startup sequence on the pipe

```
server → helper   {"mode":"session","user":"chandler","password":"…","remoteAddr":"…"}\n
helper → server   {"ok":false,"prompt":{"text":"Verification code:","echo":false}}\n   (only if PAM asks more, D43)
server → helper   {"answer":"123456"}\n                                                  (the browser's answer)
helper → server   {"ok":true,"uid":1000}\n                                               (authenticated)
server → helper   {"start":true}\n                         (false: the user has a session; the helper exits)
helper → server   {"ok":true,"uid":1000}\n                                               (session opened)
                  (helper execs the worker as the user, same pipe)
worker → server   {"protocol":1,"version":"…","user":"chandler","uid":1000,"home":"…"}\n
                  … yamux from here on: server = client, worker = server
```

`internal/protocol` defines these messages. The server refuses a worker with a
different protocol version, which matters once workers run on other hosts.

Both helper and worker call `stdio.ProtectStdout()` at startup: fd 1 is
duplicated for the protocol and then pointed at stderr, so stray prints from
Go, C libraries or PAM modules land in the log instead of corrupting the
stream. Helper and worker stderr is collected by the server and logged with
the user name.

## Why stdin/stdout + yamux + HTTP

- One pipe per session, many independent streams, each with its own flow
  control: a large download never stalls terminal keystrokes.
- HTTP inside each stream means the worker's API is the browser's API. The
  server is just an authenticating reverse proxy, and Go's standard library
  provides Range requests (`http.ServeContent`), streaming and WebSockets.
- SSH gives the same stdin/stdout byte stream, so later the server can run
  `tilde worker` on another machine over SSH with no protocol changes.
- The worker can be tested without root: `worker_test.go` runs it over an
  in-memory pipe.

## Sessions (decision 4)

- One worker per **user**, shared by every browser tab and device of that
  user. Starting a long job on a laptop and checking it on a phone works.
- Every login goes through PAM, even when a session is already running.
- **Log out** ends the session for all devices: the worker exits, shells get
  SIGHUP, the helper closes the PAM session. Background processes started with
  `nohup` keep running, as on Mint.
- **Closing the tab** leaves the session running, like walking away from the
  machine.
- Tokens: 256-bit random, `HttpOnly; Secure; SameSite=Strict` cookie named
  `__Host-tilde`, stored server-side by hash, revoked on logout, 12-hour
  absolute lifetime from login or the last unlock.
- **Lock** (D40) is per browser and kept by the server (`tokens.go`): after
  the user's idle delay without input reports, or Lock Screen on any of the
  user's browsers. Shells and jobs keep running. Unlocking checks the
  password through the helper's `verify` mode and replaces the token.
- A server restart ends all sessions (the pipes close). The frontend gets
  `401 session_ended` and returns to the login screen.

## Frontend

```
App.vue ── LoginScreen (logged out)
       ├── LockScreen (locked: clock, password, Switch User)
       └── Desktop
            ├── DesktopIcons      Computer, Home, Trash, ~/Desktop (grid layout: shell/desktopLayout.ts)
            ├── WindowLayer
            │    └── WindowFrame × n ── <app component :ctx>
            │         (a modal dialog window, the file chooser, sits above its parent)
            ├── Notices
            ├── JobsPanel ── ConflictDialog   file job progress and questions
            ├── Panel ── StartMenu, launchers, WindowList, tray (browsers, user, clock)
            └── Dialogs                       ctx.confirm / ctx.prompt
```

- **Window manager** (`src/stores/windows.ts`, `src/wm/`): a Pinia store of
  windows (id, app, title, icon, rect, state, z, args). Drags move the frame
  with a CSS transform and commit on release; resizes use eight handles with
  per-app minimum sizes. While dragging, a transparent shield covers
  everything so iframes cannot swallow pointer events. Double-clicking the
  title bar maximizes; dragging a maximized window restores it under the
  pointer, like Cinnamon. A window can open another as its modal dialog
  (`openDialog`, D49): the dialog stays above it, hides and closes with it
  and is left out of the window list and the saved layout, and the parent's
  content is `inert` until it closes.
- **Layout persistence**: the store saves windows to `localStorage` per user.
  Each window keeps launch args plus state the app saves (`ctx.saveState`), so
  a terminal window remembers its terminal id and Files remembers its folder.
  Running things live in the worker; the layout lives in the browser (4b).
- **Apps** (`src/apps`): each has a manifest in `registry.ts` (id, name, icon,
  category, MIME types, sizes, lazy component) and receives the shell API as a
  `ctx` prop (`types.ts`): set title, close, save state, close guard, launch
  another app, open a file with its registered app, notify, confirm and
  prompt dialogs, the file chooser (`ctx.chooseFile`, `src/apps/chooser`,
  D49), the shared file clipboard, home folder, focus and drag state.
- **File jobs** (`src/stores/jobs.ts`, `src/shell/JobsPanel.vue`): the store
  mirrors the worker's jobs over the events WebSocket (`src/api/events.ts`);
  the panel shows progress and the conflict dialog, and folders a job touched
  reload when it ends.
- **Folder changes** (`src/lib/fswatch.ts`): Files and the desktop register
  the folders they show (`useWatchedDirs`); the events socket sends the set
  to the worker and turns its `fs` events into `emitFsChanged`
  (`src/lib/bus.ts`), the same signal the shell's own file operations raise.
- **Desktop background** (`src/stores/background.ts`): Cinnamon's or Tilde's
  own background (D37), turned into CSS on the desktop; the Backgrounds app
  (`src/apps/backgrounds`) changes it. Files and the desktop share `src/lib/fileops.ts`
  (paste, trash, permanent delete) and `src/lib/dnd.ts` (drag and drop).
- **Files views** (`src/apps/files/ItemsView.vue`): a virtualized grid and
  list with fixed cell sizes per zoom level, so huge folders render only what
  is visible; sorting in `files/types.ts`; thumbnails via `FileIcon.vue`.
- **Text editor** (`src/apps/editor`, D48): CodeMirror 6 (`setup.ts`), one
  document per window (`document.ts`): it drafts unsaved changes to the
  worker as the user types and before Lock Screen (`src/lib/beforeLock.ts`),
  reopens them after a reload or lock, and saves against the version it
  opened.
- **Two-factor** (D43): a login or unlock answer may be `{prompt}` instead of
  the session; the session store keeps it, and the login and lock screens
  show it (`shell/AuthQuestion.vue`) until `POST /api/auth/answer` finishes.
- **Session expiry**: any `401` with `session_ended` / `not_logged_in` from
  `/api/v1/*` makes the session store return to the login screen with a
  message (`src/api/client.ts`).
- **Lock screen** (D40): `src/lib/activity.ts` reports input to the server,
  which keeps the idle clock. A `423 locked` from any request (the events
  socket reconnects as soon as a lock closes it) switches the session store
  to `locked`; the desktop is unmounted and its stores reset, as on logout,
  and the window layout comes back from `localStorage` on unlock.
- **Theme**: design tokens in `src/styles/themes/mint-y-dark.css`, layout in
  `src/theme/desktopStyle.ts`. See `docs/theming.md`.
- **Icons**: bundled Mint-Y PNGs resolved by freedesktop name through
  `/icons/index.json`, with generic built-in glyphs as a fallback.

## Where to add things

| You want to… | Go to |
| --- | --- |
| Add a filesystem or process operation | `server/internal/worker` (+ test, `docs/api.md`, `web/src/api`) |
| Add a long-running file operation | a job op in `worker/jobs.go`; progress reaches the shell by itself |
| Change login, cookies, origin checks | `server/internal/server` (read `docs/security.md` first) |
| Change what the session looks like to PAM/logind | `server/internal/helper`, `packaging/pam.d/tilde` |
| Add an app | `web/src/apps/<name>/` + `web/src/apps/registry.ts` |
| Change the panel, menu or desktop | `web/src/shell` |
| Change window behavior | `web/src/stores/windows.ts`, `web/src/wm` |
| Change colors or sizes | `web/src/styles/themes/*.css` |
