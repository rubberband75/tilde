# Roadmap

Phases from the plan. `[x]` done, `[~]` partly done, `[ ]` not started.
Pick the next unchecked item in the lowest phase unless there is a reason not
to; tick items off as they land.

## Phase 1 — Spike: login, session, one terminal

- [x] PAM login through a root session helper (`internal/helper`, `internal/pamauth`)
- [x] logind session of type `web` via pam_systemd (`/etc/pam.d/tilde`)
- [x] Worker as the user (UID, GID, groups), clean environment
- [x] stdin/stdout + yamux + HTTP between server and worker, version handshake
- [x] Reverse proxy from `/api/v1/*` to the worker, WebSockets included
- [x] Terminal with ring-buffer replay and forced redraw on reattach
- [x] "Session ended" signal so the browser returns to login
- [x] Session tokens (HttpOnly, SameSite=Strict, server-side, revocable), 12 h lifetime
- [x] Origin checks on state-changing requests and WebSocket upgrades
- [x] Login rate limiting per address
- [x] Self-signed TLS with fingerprint in the log; `--dev` plain HTTP on loopback
- [x] Single-user dev mode without root
- [x] e2e tests (real PAM, real setuid) — run in a VM
- [x] Scripted disposable e2e VM: `make e2e-vm` (Ubuntu 24.04, Mint 22's base; D29)
- [ ] Run the e2e suite in the VM and fix whatever differs from the sandbox it was written in

## Phase 2 — Shell UI

- [x] Mint-Y Dark design tokens from the upstream theme sources
- [x] Bottom panel: menu button, launchers, window list, user, clock
- [x] Start menu: favorites, session buttons, categories, search
- [x] Window manager: drag (transform), 8 resize handles, min size, focus/z-order, minimize, maximize, double-click to maximize, drag out of maximized
- [x] Interaction shield over iframes while dragging
- [x] Layout persisted per user, restored on reload
- [x] Notifications (simple toasts)
- [x] Bundled Mint-Y icons with fallback glyphs
- [ ] Panel: system tray applets (sound, network) — needs D-Bus in the worker
- [ ] Calendar popup on the clock
- [ ] Keyboard shortcuts (Alt+Tab equivalent, Super for menu) — browser grabs many; add alternates or Keyboard Lock in fullscreen
- [ ] Edge snapping and tiling
- [ ] Window menu (right-click title bar: Always on top, Move, Resize, Close)
- [ ] Animations for minimize/maximize
- [ ] Ubuntu font check / fallback metrics

## Phase 3 — Nemo clone

- [x] Places sidebar from XDG user dirs; Computer; Trash
- [x] Breadcrumb path bar, back/forward/up, keyboard navigation
- [x] Icon view and list view, hidden files toggle (Ctrl+H)
- [x] Selection: click, Ctrl, Shift ranges, Ctrl+A
- [x] New folder with inline rename; rename (F2)
- [x] Move to trash (Delete), restore, empty trash
- [x] Upload (picker and drag-and-drop, with progress), download
- [x] Open in Terminal
- [ ] **File-operation jobs**: copy/move/delete trees in the worker with progress, cancel, and Replace/Skip/Rename prompts; cut/copy/paste
- [ ] Permanent delete (Shift+Delete) with confirmation
- [ ] Drag and drop between windows and to the desktop
- [ ] Thumbnails (freedesktop thumbnail cache, host thumbnailers)
- [ ] Properties dialog (permissions, owner, size of folders)
- [ ] Huge directories: stream or paginate the listing
- [ ] Sorting options, zoom levels, search
- [ ] MIME sniffing for files without a known extension
- [ ] Bookmarks (`~/.config/gtk-3.0/bookmarks`)

## Phase 4 — Desktop

- [x] Computer, Home, Trash icons; files from the XDG Desktop folder
- [x] Desktop context menu: New Folder, Open in Terminal, Refresh
- [x] Drop files on the desktop to upload
- [x] Trash icon shows full/empty
- [ ] **Control WebSocket with inotify events** (replace the 10 s polling in DesktopIcons and refresh in Files)
- [ ] Remember icon positions (Nemo stores them as gvfs metadata)
- [ ] Wallpaper (from the user's Cinnamon settings or a picker)
- [ ] Per-drive trash; mounts and removable drives via udisks2

## Phase 5 — Hardening

- [ ] Lock screen following Cinnamon's idle/lock settings; unlock through helper `verify` mode
- [ ] Idle detection across all clients
- [ ] Revocable device tokens ("remember this device"), device list
- [ ] Multi-step PAM conversations (TOTP 2FA)
- [ ] Polkit authentication agent in the browser
- [ ] Audit log (logins, commands started, deletions)
- [ ] Tray indicator for connected clients
- [ ] Review: CSP, headers, cookie flags, error messages that leak information

## Phase 6 — Apps

- [ ] Text editor (CodeMirror or Monaco)
- [ ] Media player (uses `fs/content` Range requests)
- [ ] Image viewer
- [ ] Process manager (`/proc`, signals; systemd units over D-Bus)
- [ ] System monitor tray applet (CPU, memory, disk, network). Always send full snapshots.
- [ ] Local web apps: list listening ports from `/proc/net/tcp`, open code-server/Jupyter in a window, **served from a separate origin**
- [ ] Shared file chooser dialog for apps
- [ ] Settings app: theme, wallpaper, saved per user in `~/.config/tilde/`

## Phase 7 — Remote windows

- [ ] Xpra HTML5 client in a window; launch host `.desktop` apps from the menu

## Phase 8 — Multi-machine

- [ ] `tilde worker` over SSH to other hosts, machine list
- [ ] Optional broker/relay with end-to-end encryption

## Packaging

- [x] systemd unit without sandboxing, `/etc/default/tilde`, PAM file, `make install`
- [ ] `.deb` package (depends on libpam0g), release workflow
