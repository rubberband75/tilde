# Decisions

Every significant decision, newest at the bottom, with the reason and what
was rejected. Add new entries at the end; when a decision is replaced, mark
the old one "Superseded by Dnn" instead of deleting it.

Format: **Dnn — title** (date, status). Decision. Why. Rejected.

---

**D1 — Not VNC: the browser renders the desktop** (2026-10-14, accepted)
The desktop, windows and apps are HTML in the browser; the host exposes an
API. Why: crisp UI, works on slow links, file operations are native rather
than screen-scraped. Rejected: VNC/RDP-style remote framebuffer for the whole
desktop. Remote graphical *apps* come later through Xpra (D26).

**D2 — Kernel-enforced permissions, no app-level access modes** (2026-10-14, accepted)
Everything runs as the logged-in Linux user; the kernel decides. Why: one
security model that already works and is already understood. Rejected:
"read-only / read-write / execute / sudo" access modes enforced by Tilde.

**D3 — Backend in Go** (2026-10-14, accepted)
Single static-ish binary, good PTY support, cgo for PAM.

**D4 — Frontend: Vue 3 + Vite + Pinia + TypeScript** (2026-10-14, accepted)
Rejected: Nuxt (no SSR/SEO need; windows are state, not routes; Nitro would
duplicate the Go server) and petite-vue (built for sprinkling onto server
pages; no real components, render functions or transitions).

**D5 — UI primitives: Reka UI, unstyled** (2026-10-14, accepted)
Accessible menus, context menus and popovers without a theme to fight.
Rejected: Nuxt UI (its look fights Mint-Y; Tailwind-only).

**D6 — Own window manager** (2026-10-14, accepted)
A Pinia store plus a frame component. Why: the store, taskbar integration,
focus rules and theming are the product; a library would only save the drag
math. interact.js stays an option for drag/resize/snapping math. Rejected:
WinBox.js (manages its own DOM, awkward with Vue reactivity).

**D7 — Own CSS library with design tokens** (2026-10-14, accepted)
All values are CSS custom properties; components use tokens only. v1 targets
Mint-Y Dark closely. A "desktop style" is tokens plus a layout config (panel
position, window button order, dock), so Ubuntu or Pop!_OS styles are
possible later. Saved themes later live in `~/.config/tilde/`.

**D8 — Authentication through PAM directly** (2026-10-15, accepted)
Why: a near-full desktop needs a real login session (logind session,
XDG_RUNTIME_DIR, session D-Bus, keyring unlock, polkit agent), control over
the session type and environment, and no dependency on sshd (not installed by
default on Mint). Rejected: SSH to localhost (would still need our own worker
over SSH; at the mercy of sshd config; no inotify/trash/D-Bus via SFTP). SSH
remains the planned transport for *other* machines (D23).

**D9 — Root session helper per login; worker as the user** (2026-10-15, accepted)
The helper calls `pam_authenticate`, `pam_acct_mgmt`, `pam_setcred`,
`pam_open_session` as root, starts the worker with `SysProcAttr.Credential`
(UID, GID, supplementary groups) and a clean environment, and stays alive to
call `pam_close_session`. Never setuid inside the multi-threaded server.
`XDG_SESSION_TYPE=web` and `XDG_SESSION_CLASS=user` are set before opening
the session so pam_systemd registers it. The helper exists as a separate
process because pam_systemd registers *the calling process* as the session.

**D10 — Server ↔ worker: stdin/stdout + yamux + plain HTTP** (2026-10-15, accepted)
Why: independent streams with per-stream flow control; the worker's HTTP API
is the browser's API (server = authenticating reverse proxy); Range requests
and WebSockets for free; identical over SSH later; the worker never listens on
a port. Rules: stdout carries only the protocol; version handshake first;
keepalives detect a dead worker; stream window raised to 16 MiB. Rejected:
JSON-lines protocol with hand-built multiplexing (Cockpit style), gRPC.
Possible later: Unix socket with `SCM_RIGHTS` for zero-copy local streaming.

**D11 — Browser ↔ server: REST + WebSockets, JSON; raw binary frames for terminals** (2026-10-19, accepted)
One WebSocket per terminal now; a control WebSocket for events later.

**D12 — One worker per user, shared by all clients** (2026-10-19, accepted, 4a)
Rejected: one worker per login (separate desktops per device).

**D13 — Running things in the worker, window layout in the browser** (2026-10-19, accepted, 4b)
Windows have stable ids and save the worker resource they show (e.g.
`terminalId`), so syncing layout server-side later is easy.

**D14 — Terminal persistence: ring buffer + forced redraw** (2026-10-19, accepted, 4c)
1 MiB per PTY; reattach replays it, then the first resize forces SIGWINCH.
Terminals live until the shell exits or logout. Shells start as login shells
(`-bash`) so `~/.profile` runs, which on Mint also sources `~/.bashrc`.
Later: a Go VT emulator (charmbracelet/x/vt) for exact screen state.

**D15 — Match Mint's session behavior** (2026-10-19, accepted, 4d)
Log out ends the session for all devices (SIGHUP to shells, nohup jobs
survive, PAM session closed). Lock follows the user's Cinnamon idle/lock
settings via gsettings (`org.cinnamon.desktop.session idle-delay`,
screensaver `lock-enabled`, `lock-delay`) — not implemented yet. Closing the
tab leaves the session running; no auto-logout by default.

**D16 — Tokens** (2026-10-19, accepted, 4e)
Random 256-bit token in an `HttpOnly; Secure; SameSite=Strict` cookie
(`__Host-tilde`), stored server-side by hash, rotated on every login and
unlock, revoked on logout. Idle (no input from any client) → lock.
Absolute lifetime 12 hours (open question: confirm). Concurrent clients
allowed, with a tray indicator later.

**D17 — Server restart ends sessions in v1** (2026-10-19, accepted, 4f)
The worker connection sits behind the session manager, so moving pipe
ownership to the helper (to survive upgrades) is a contained change later.

**D18 — App model** (2026-10-19, accepted, 5a–5g)
Manifest (id, name, comment, icon, category, MIME types, single-instance,
sizes, lazy component); built-in apps only in v1, runtime plugins later as
admin-installed trusted code; a shell API passed as `ctx` (window handle,
args, save/restore state, close guard, launch, open-with, notify, later a
shared file chooser); general worker APIs that apps combine, app-specific
endpoints under `/apps/<id>/` only when needed; web apps only in the menu for
v1, host `.desktop` apps via Xpra later; MIME detection from the host's
shared-mime-info (Go's `mime` package reads its globs), honoring
`~/.config/mimeapps.list` later.

**D19 — Name: Tilde** (2026-10-20, accepted)
`~`, the home directory. Supersedes "webde" (searches collide with the
German portal WEB.DE). Paths: `/etc/pam.d/tilde`, `~/.config/tilde/`,
`/var/lib/tilde`.

**D20 — Bundle the Mint-Y icons** (2026-10-20, accepted)
Copied by `scripts/fetch-icons.sh` into `web/public/icons/Mint-Y` with their
license and credits. Note: the theme is CC BY-SA 4.0 (some icons GPLv3), not
GPL as first assumed; shipping them as separate assets with their notices is
fine alongside AGPL code.

**D21 — License: AGPL-3.0-or-later** (2026-10-20, accepted)
Tilde is a network service; AGPL requires anyone running a modified version
for others to share their changes. Supersedes GPL.

**D22 — Security baseline** (2026-10-20, accepted)
See `docs/security.md`. Includes, from studying webdesktopd (D25): never store
passwords in the browser; server-side revocable tokens, never in URLs; no
systemd mount sandboxing on the unit; local web apps on a separate origin;
a real-privilege VM test harness.

**D23 — Multi-machine later, via the worker over SSH** (2026-10-14, accepted)
No central server for v1. Use Tailscale or a tunnel to reach machines behind
NAT. Later: run `tilde worker` on other hosts over SSH; optionally a broker or
relay (with end-to-end encryption if it is ours).

**D24 — Default exposure: localhost, self-signed TLS** (2026-10-20, accepted)
`--listen 127.0.0.1:8443` by default, self-signed certificate in
`/var/lib/tilde/tls` with its fingerprint logged. Reach it over a VPN or LAN
on purpose. `--dev` serves plain HTTP on loopback only.

**D25 — Lessons from webdesktopd** (2026-10-20, accepted)
See `docs/lessons-from-webdesktopd.md`. Tilde differs on purpose: PAM not
SSH; all file operations in a worker running as the user; HTTP-over-yamux not
a custom binary frame protocol; real window manager and Cinnamon shell.

**D26 — Remote graphical app windows: Xpra HTML5, later** (2026-10-14, accepted)
Xpra forwards individual windows, so each fits in a Tilde window. GTK
Broadway is a lighter GTK-only alternative; WebRTC if low-latency video is
needed.

**D27 — Root login disabled** (2026-10-20, accepted)
The helper refuses "root" before PAM and any UID 0 account after lookup. Log
in as a user and use sudo.

**D28 — Development without root: single-user mode** (2026-10-20, accepted)
When `tilde serve --dev` runs without root, only the account that started it
can log in; PAM still checks the password (pam_unix's helper can verify your
own password), no PAM session is opened, and the worker runs as you. This
makes UI work possible without sudo; anything about sessions and privilege
needs root, in a VM.

**D29 — E2E VM: Ubuntu 24.04 cloud image under QEMU, scripted** (2026-10-20, accepted)
`scripts/e2e-vm.sh` (`make e2e-vm`) boots the Ubuntu 24.04 cloud image with
cloud-init under plain QEMU/KVM, installs the host's build with the real PAM
file and systemd unit, and runs the e2e suite over SSH. Why: Mint 22 is built
on 24.04 (same PAM, systemd, logind, glibc), and a cloud image needs no
interactive install, so the run is repeatable and an agent can run it too.
Plain QEMU needs only `/dev/kvm` access, no libvirt group. Rejected: a Mint
VM in virt-manager as the e2e target (manual install, not scriptable; still
the place to check Cinnamon-specific behavior by hand); containers (no real
logind session or PAM stack).

**D30 — Repository, CI and frontend tooling** (2026-10-20, accepted)
Public repository at github.com/rubberband75/tilde with GitHub Actions
running `make test` and the builds (Ubuntu 24.04, Node 22). Frontend unit
tests with Vitest + jsdom next to the code; Prettier for formatting (no
semicolons, single quotes, 120 columns, matching the existing code).
`make test` fails on unformatted Go or frontend code. No ESLint for now:
strict TypeScript through vue-tsc covers most of what it would catch.

**D31 — File operations run as worker jobs** (2026-10-20, accepted)
Copy, move and delete are background jobs in the worker (`/api/v1/jobs`),
with progress pushed over a new events WebSocket (`/api/v1/events`, the
control socket D11 planned; inotify joins it in phase 4) and conflicts that
any of the user's clients can answer. Spec choices, close to Nemo/GIO with a
few simplifications: folder-over-folder offers Merge/Skip/Rename (never
Replace, which would silently delete a tree); a file never replaces a folder;
"apply to all" is per kind (file or folder); errors on single items do not
prompt (GIO asks Skip/Retry) but are collected and shown when the job ends;
links are copied as links and special files skipped; permission bits and
mtimes are kept, ownership and setuid are not; files are written to a
temporary name and committed atomically, so a cancelled copy leaves no
partial file and a replaced file survives a failed copy; moves use
`renameat2(RENAME_NOREPLACE)` and fall back to copy-then-delete across
filesystems; drag and drop (`drop`) moves within a filesystem and copies
across, like Nemo. Logging out cancels running jobs. Rejected: a synchronous
copy endpoint (blocks a request for minutes, no progress or conflicts),
polling for progress (the events socket is needed for inotify anyway).

---

## Open decisions

| Question | Options | Leaning | Needed by |
| --- | --- | --- | --- |
| Absolute session lifetime | 12 h re-login vs lock-only like Mint | 12 h, confirm | Phase 5 |
| Freedesktop compatibility beyond trash | thumbnail cache spec (`~/.cache/thumbnails`) and host thumbnailers; drives via udisks2 over D-Bus | follow the specs | Phase 3–4 |
| Per-drive trash | `$topdir/.Trash-$uid` for other filesystems | implement with udisks | Phase 4 |
| 2FA through PAM | multi-step PAM conversation round-trips to the browser | needed for TOTP modules | Phase 5 |
| Lock screen | gsettings-driven idle lock, unlock via `verify` helper mode | per D15 | Phase 5 |
| Events | inotify and notifications on the events WebSocket (job progress done, D31) | per D11 | Phase 4 |
| Packaging | `.deb` with systemd unit (depends on libpam0g); update method | nfpm or debhelper | Before first release |
| Multi-machine | third-party tunnel vs own relay; E2E encryption | worker over SSH first | Phase 8 |
