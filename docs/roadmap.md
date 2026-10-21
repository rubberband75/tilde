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
- [x] Run the e2e suite in the VM (passed unchanged), plus logind checks: session type `web`, runtime dir, session D-Bus, logout closes the session, nohup jobs survive
- [x] Playwright UI suite (`web/e2e`) run by `make e2e-vm` after the Go suite: login, desktop, Files, terminal (D35)

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
- [x] **File-operation jobs**: copy/move/delete trees in the worker with progress, cancel, and Replace/Skip/Rename/Merge prompts; cut/copy/paste (D31)
- [x] Permanent delete (Shift+Delete, Delete in the trash) with confirmation; delete offered for items that cannot be trashed
- [x] Drag and drop between windows, the sidebar, the desktop (folders, Home, Trash) and terminals (types the quoted paths)
- [x] Thumbnails (freedesktop thumbnail cache shared with Nemo, host thumbnailers, built-in PNG/JPEG/GIF scaler)
- [x] Properties dialog: type, size or live folder contents, location, link target, free space, dates; permissions as in Nemo (Alt+Enter)
- [x] Huge directories: streamed listing (NDJSON batches), virtualized grid and list (50 000 entries in about half a second)
- [x] Sorting (name, size, type, date; reversed; natural order; folders first), zoom levels (Ctrl+±, Ctrl+wheel), arrow-key navigation
- [x] Search (Ctrl+F): recursive name search streamed from the worker, Location column
- [x] MIME sniffing for files without a known extension (budgeted in listings)
- [x] Bookmarks (`~/.config/gtk-3.0/bookmarks`, shared with Nemo): sidebar section, Ctrl+D, rename, remove, drop onto

## Phase 4 — Desktop

- [x] Computer, Home, Trash icons; files from the XDG Desktop folder
- [x] Desktop context menu: New Folder, Open in Terminal, Refresh
- [x] Drop files on the desktop to upload
- [x] Trash icon shows full/empty
- [x] **Control WebSocket with inotify events**: clients name the folders they show, the worker watches them and pushes changes; replaces the desktop's 10 s polling (D34)
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
- [x] CI on GitHub Actions: format checks, Go and frontend tests, builds (D30)
- [ ] `.deb` package (depends on libpam0g), release workflow
