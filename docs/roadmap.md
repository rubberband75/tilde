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
- [x] Keyboard shortcuts (D53): Cinnamon's keys in fullscreen with Keyboard Lock (panel and menu button), Alt+Shift alternates in any browser; Alt+Tab switcher, Super+arrows tiling, Show Desktop, Alt+F4, window menu, Move/Resize
- [x] Edge snapping and tiling (D52): halves at the side and top edges, quarters at the corners; a preview while dragging; the window's own size back when dragged out; kept through reloads
- [x] Window menu (D52): right-click the title bar for Minimize, Maximize, Move, Resize (pointer or arrow keys), Always on Top, Close
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
- [x] Remember icon positions: drag to any grid cell, saved per user, first layout taken from Nemo's; Organize Desktop by Name (D36)
- [x] Wallpaper: the user's Cinnamon background by default; a Backgrounds picker (Cinnamon's collections, Pictures, added folders; aspect, gradient, colors) saves Tilde's own (D37)
- [x] Per-drive trash (`$topdir/.Trash/$uid` or `.Trash-$uid`, as GIO), listed, restored and emptied with the home trash (D38)
- [x] Drives via udisks2 (D39): Computer lists mounted and unmounted volumes, a Devices section in Files, Unmount and Eject, live updates from udisks signals. Mounting came with the polkit agent (phase 5, D44)

## Phase 5 — Hardening

- [x] Lock screen following Cinnamon's idle/lock settings; unlock through helper `verify` mode (D40): kept by the server per browser, Lock Screen (menu, Ctrl+Alt+L) locks every browser, unlock replaces the token
- [x] Idle detection: each browser locks after the user's idle delay (D40); logind's `LockedHint` follows the lock and `loginctl lock-session` locks every browser (D41). logind refuses `IdleHint` for web sessions
- [x] Remember the user name on the login screen (opt-in, `localStorage`); no device tokens that save or skip the password (D42)
- [x] Multi-step PAM conversations for two-factor (D43): PAM's extra prompts go to the login and lock screens; documented, not enabled by default; tested in the VM with `pam_oath`
- [x] Polkit authentication agent in the browser (D44): the worker registers for its web session and runs polkit's helper; the dialog shows in every browser. Files mounts drives through it
- [x] Audit log (D46): the system journal, tagged `tilde-audit` (also in `/var/log/auth.log`): logins, locks, sessions, terminals, deletions, mounts and polkit authentications, written by root
- [x] Tray indicator for connected clients (D47): the panel lists the user's signed-in browsers (where from, which kind, open or locked) and signs one out
- [x] Review: CSP, headers, cookie flags, error messages that leak information (D45): auth-stack refusals look like wrong passwords, `--trust-proxy` takes the proxy's own address, CORP and (with your own certificate) HSTS, worker responses cannot set cookies and are not cached by default

## Phase 6 — Apps

- [x] Text editor (D48): CodeMirror 6, opened from Files (text types, and Open With Text Editor), syntax highlighting, find and replace; unsaved changes kept as drafts by the worker through reloads and locks; saves keep symlinks, permissions and hard links, and ask before overwriting a file changed on disk
- [x] Shared file chooser for apps (D49): `ctx.chooseFile` opens a GTK-style chooser as a modal dialog window (open, save, select folder; filters, several files, typed locations); the text editor's Open and Save As use it
- [ ] Text editor: reopening untitled drafts whose window is gone, tabs
- [x] Media player (D51): sound and video from Files in one `<video>` element over `fs/content` Range requests; the folder as the playlist, the position kept through reloads and locks, media keys, fullscreen
- [x] Image viewer (D50): opened from Files for the image types browsers draw; the folder's images in order, Best Fit, zoom and drag, turning the view, Move to Trash, fullscreen and a slideshow
- [ ] Process manager (`/proc`, signals; systemd units over D-Bus)
- [ ] System monitor tray applet (CPU, memory, disk, network). Always send full snapshots.
- [ ] Local web apps: list listening ports from `/proc/net/tcp`, open code-server/Jupyter in a window, **served from a separate origin**
- [ ] Settings app: theme, wallpaper, lock timeout (follow Cinnamon, the default, or Tilde's own; D40), saved per user in `~/.config/tilde/`; recent logins and failed attempts from the audit log (D46), served by the server

## Phase 7 — Remote windows

- [ ] Xpra HTML5 client in a window; launch host `.desktop` apps from the menu

## Phase 8 — Multi-machine

- [ ] `tilde worker` over SSH to other hosts, machine list
- [ ] Optional broker/relay with end-to-end encryption

## Packaging

- [x] systemd unit without sandboxing, `/etc/default/tilde`, PAM file, `make install`
- [x] CI on GitHub Actions: format checks, Go and frontend tests, builds (D30)
- [ ] `.deb` package (depends on libpam0g), release workflow
