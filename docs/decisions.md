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

**D16 — Tokens** (2026-10-19, accepted, 4e; idle locking and the 12 hours amended by D40; no device tokens, D42)
Random 256-bit token in an `HttpOnly; Secure; SameSite=Strict` cookie
(`__Host-tilde`), stored server-side by hash, rotated on every login and
unlock, revoked on logout. Idle (no input from any client) → lock.
Absolute lifetime 12 hours (confirmed by D69). Concurrent clients
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

**D32 — PAM runs on the helper's main thread** (2026-10-20, accepted)
`cmd/tilde` locks the main goroutine to the main OS thread in `init`, and the
helper calls PAM from it. Why: pam_loginuid writes `/proc/self/loginuid`,
which the kernel accepts only from the thread-group leader; from another Go
thread it fails with EPERM, the `required` module fails
pam_open_session, and the login is refused at random (one in five to one in
forty in the e2e VM). Found by the e2e VM; `TestRepeatedLogins` guards it.
Rejected: making pam_loginuid `optional` (loses the audit login uid and
session id, which logind and auditing rely on).

**D33 — Thumbnails follow the freedesktop spec** (2026-10-20, accepted)
The worker makes thumbnails on demand into `~/.cache/thumbnails` exactly as
the spec says (MD5 of the GLib-escaped file URI, `Thumb::URI` and
`Thumb::MTime`, 0600 files, failures recorded under `fail/`), so Nemo and
Tilde share one cache. Generation uses the host's `*.thumbnailer` files, as
Nemo does, with a built-in scaler for PNG, JPEG and GIF when none is
installed (no EXIF rotation). Thumbnailers run as the user on possibly
hostile files, as under Nemo, two at a time, in their own process group,
killed after 20 s. Rejected: a private cache (duplicates Nemo's work and
disk), generating in the browser (would download whole files).

**D34 — Folder changes: clients name what they show, the worker uses inotify** (2026-10-21, accepted)
Each client sends the folders on its screen over the events socket
(`{"type":"watch","dirs"}`, replacing its previous set); the worker keeps
one non-recursive inotify watch per folder, counted across clients, and
publishes changed folders as `fs` events after collecting changes for
200 ms. Reads are not changes (no `IN_ACCESS`/`IN_CLOSE_NOWRITE`), so the
worker's own listing, sniffing and thumbnailing never cause a reload, and
file contents count when a writer closes (`IN_CLOSE_WRITE`), not on every
write. The shell's own operations still announce their result at once, and
Files never cuts a running listing short for a change (it reloads once
more afterwards), so a folder that keeps changing still refreshes. After a
reconnect the client resends its set and reloads, since missed changes are
not replayed. Why: the folders on screen are exactly the ones whose changes
matter, a watch per folder is cheap, and the events socket already exists
(D11, D31). Rejected: polling (the desktop polled every 10 s); recursive
watches (a watch per subfolder of a home folder can exhaust
`max_user_watches`, and a window shows one folder); fanotify (needs
`CAP_SYS_ADMIN`); per-event messages (a big copy would flood the socket).

**D35 — UI tests: Playwright on the host against the e2e VM** (2026-10-21, accepted)
`web/e2e` holds Playwright tests (`@playwright/test`, Chromium) for the real
UI against a real install: login and logout, desktop icons, Files, live
folder updates, trash, the terminal. `make e2e-vm` runs them after the Go
suite, from the host through an SSH tunnel to the VM, logged in as the VM's
throwaway `tester`; `scripts/e2e-vm.sh ui` runs only them, and `make
e2e-ui` runs them against any URL. They share one user, so they run one at
a time, and each cleans up the files it makes. Why: the VM already has the
real PAM, logind and privilege drop, and running the browser on the host
avoids putting Node and Chromium in the VM. Rejected: Playwright inside the
VM (a large download into a disposable image on every reset); UI tests in
CI against `make dev-server` (no PAM login without a password, and
single-user mode skips the session code the UI depends on); Vitest browser
mode or Cypress (Playwright handles WebSockets, multiple pages and traces
well and needs no running dev server).

**D36 — Desktop icon layout: Tilde's own grid, seeded from Nemo** (2026-10-21, accepted)
Icons sit on a grid of theme-sized cells. The worker stores cells per icon
(file name, or a URI for Computer/Home/Trash) in
`~/.config/tilde/desktop-layout.json`; saves merge, so clients moving
different icons keep each other's moves. Until the user arranges icons in
Tilde, the layout starts from Nemo's: per-file GVfs metadata
(`metadata::nemo-icon-position`, read with `gio info`, which reads the
metadata database without the daemon) and `~/.config/nemo/desktop-metadata`
for the built-in icons, converted to cells with Nemo's grid size or the
smallest gap between icons. Tilde never writes Nemo's positions. Icons
without a cell, or whose cell is outside a smaller window or taken, fill
free cells top to bottom, then left to right; a saved cell outside the
window is kept for when it grows. Dragging icons moves them as a group to
the cell under the dragged icon (the nearest free cell on a collision);
"Organize Desktop by Name" clears the layout. Renaming a desktop file
loses its position, as the layout is keyed by name. Why: a browser window
is rarely the screen Nemo arranged for, so shared pixel positions would
fight; seeding keeps a Mint user's arrangement on first use (chosen by the
user over a Tilde-only layout and a fully shared one). Rejected: writing
Nemo's metadata (moves in a small browser window would rearrange the real
desktop, and it needs gvfsd-metadata over D-Bus); pixel positions (break
when the window size changes).

**D37 — Desktop background: Cinnamon's by default, Tilde's own once picked** (2026-10-21, accepted)
The desktop shows the user's Cinnamon background
(`org.cinnamon.desktop.background`: picture, aspect, colors, gradient),
read with `gsettings` (dconf's database, no session bus needed), drawn
with CSS as Cinnamon draws it. The Backgrounds app (desktop menu: Change
Desktop Background) lists what Cinnamon's Backgrounds settings lists
(the wallpaper XML lists, Pictures, added folders) and saves a choice as
Tilde's own setting in `~/.config/tilde/desktop-background.json`; "Use
Cinnamon's Background" drops it. Pictures are loaded through
`/api/v1/fs/content`, so the kernel decides what the user may show.
Changes reach other open clients on their next load, not live. Why: same
pattern as the icon layout (D36): a Mint user's desktop looks like theirs
from the first login, and trying backgrounds in a browser never changes
the real desktop. Rejected: writing Cinnamon's gsettings (needs dconf over
the session bus, and changes the real desktop); a Tilde-only background
(ignores what the user already chose); slideshow XML backgrounds (shown as
colors only for now).

**D38 — Per-drive trash as GIO does it** (2026-10-21, accepted)
Trashing a file on another drive uses that drive's trash, following the
Trash spec: `$topdir/.Trash/$uid` when `$topdir/.Trash` is a real folder
with the sticky bit, else `$topdir/.Trash-$uid`, created 0700. A trash
folder that is a symlink or owned by someone else is refused, so another
user of a shared drive cannot plant one to capture files. Info files there
store paths relative to `$topdir`, so a drive mounted elsewhere still
restores, and relative paths that leave the drive are ignored. Only
mounts in the drive list (real block devices, including loop-mounted
images but not snaps) get a trash; tmpfs and other system mounts answer
`other_drive`, and Tilde offers to delete instead, as Nemo does. The trash
API lists, restores and empties all trashes together; drive items are
named by their absolute path in the trash. Why: moving a file "home" from a
USB stick copies it across drives and fills the home disk, and Nemo and
other GIO programs already look in these folders. Mounting and removable
drives (udisks2) stay a separate item. Rejected: copying to the home trash;
waiting for udisks (per-drive trash only needs the mount table).

**D39 — Drives through udisks2 with godbus; no mounting until the polkit agent** (2026-10-21, accepted; mounting added by D44)
The worker talks to udisks2 on the system bus with `github.com/godbus/dbus/v5`
(pure Go, also what the D-Bus tray applets will need), as the user, so
udisks and polkit make every decision. It lists volumes (mounted or not,
skipping what udisks marks to ignore, swap and snaps), unmounts, and
ejects: unmount every volume of the drive, then power it off. udisks lets
users unmount their own mounts without polkit; mounting, ejecting and
powering off need an active local session or an administrator's password,
and a web session is not local. So Tilde does not offer Mount yet (opening
an unmounted volume explains how to mount it in a terminal, where
`udisksctl` asks for the password), and Eject reports the drive safe to
unplug once it is unmounted even when polkit refuses the power-off.
Requests carry `auth.no_user_interaction`, so udisks never waits for an
agent that is not there. udisks signals become `drives` events. Chosen by
the user over the alternatives below. Rejected: shelling out to
`udisksctl`/`gio` (no change signals, parsing text output); a polkit rule
letting web sessions mount (widens what a remote session can do; the e2e
VM has a test-only rule for its `tester` account to set up the test
stick); building the browser polkit agent first (phase 5; mounting comes
with it).

**D40 — Lock screen: kept by the server, per browser** (2026-10-23, accepted)
The server keeps the lock, per session token, so per browser (its tabs share
the cookie). A locked token gets `423 locked` from `/api/v1/*`, and locking
cancels the requests already made with it, which closes its terminal and
events WebSockets; only `/api/auth/*` still answers. Unlocking checks the
password through the helper's `verify` mode (PAM auth and account stacks)
under the login rate limit, and replaces the token with a new one.
Amending D16: (1) idle locks each browser on its own, not "no input from any
client", because otherwise a tab left open at home stays usable while the
user is active at work. The browser reports input at most once per
heartbeat; the server locks a token when the user's delay passes without
any, on the next request or a sweep every 5 seconds, so a tab on an
unattended machine loses its sockets too. (2) Lock Screen (menu,
Ctrl+Alt+L) locks every browser of the user, and each unlocks separately.
(3) The 12 hours count from the last time the password was typed: unlocking
starts them again. Expiry still goes to the login screen and the session
keeps running. Locking drops the desktop from the page, as logging out
does, rather than covering it: devtools would remove an overlay and could
read terminal output and file lists from memory. So apps must keep unsaved
work in the worker (the phase 6 editor's drafts). The delay follows D15:
Cinnamon's screensaver settings (`idle-delay` + `lock-delay`, if
`idle-activation-enabled` and `lock-enabled`), else GNOME's, else
Cinnamon's defaults (15 minutes); locking turned off there means Tilde
never locks on idle. The worker reads them (the server never opens user
files) and streams them to the server on `/internal/lock-settings`, a route
the proxy never forwards. A Tilde-only timeout chosen in the Settings app,
with Cinnamon's as the default, comes later (roadmap phase 6). From a
locked browser, Log Out only signs that browser out, so nobody at a locked
screen can end the session everywhere; the lock screen's Switch User uses
`/api/auth/leave`, which never ends the session, because another tab may
have unlocked the cookie meanwhile. A locked browser cannot lock the
others either. Unlock attempts share the login rate limit, counted when
they start (parallel guesses get no more), and a locked token gets 5 tries
before it is revoked. Lock responses clear the browser's HTTP cache
(`Clear-Site-Data`). Still outside the lock: files opened in their own
browser tab and the window layout in `localStorage`; the phase 6 viewers
and keeping the layout in the worker would close those. Rejected: a lock
that is only an overlay in the page; idle timers in the page (tabs sharing
a cookie would disagree); always locking after a fixed time even when the
desktop's locking is off (the Settings override covers that need).

**D41 — logind sees the lock; no idle hint for web sessions** (2026-10-23, accepted)
The web session's `LockedHint` is set while every browser of the user is
locked (or none is signed in), so `loginctl show-session` tells the truth,
and logind's `Lock` signal (`loginctl lock-session`, `lock-sessions`) locks
every browser, like the Lock Screen button. The worker does the D-Bus side
as the user, as for udisks (D39), connecting after its hello so a slow bus
never holds up a login. It follows the session in `XDG_SESSION_ID` only if
it is a `web` session of the worker's own uid: when the server is started
from inside another session (sudo in a desktop terminal), pam_systemd
joins that one instead, and Tilde must not touch it. A `Lock` counts only
from logind's bus name, followed through the bus daemon's
`NameOwnerChanged`: any user can send a signal straight to the worker, past
its match rules, and would otherwise lock another user's browsers. The
server pushes the hint over `PUT /internal/session/locked` when the lock
state of the user's tokens changes (one retry timer, backing off), and the
worker passes lock requests on the `/internal/session` stream (renamed
from `/internal/lock-settings`), read in lines of at most 4 KiB, at most
one lock a second. logind's `Unlock` signal is ignored: only the password
unlocks a browser. `IdleHint` is not reported: logind refuses it
for non-graphical sessions ("Idle hint control is not supported on
non-graphical sessions", systemd 255, checked in the e2e VM), and Tilde's
session is `web`, which is what it is. So a running Tilde session never
counts as idle for logind's `IdleAction`. Rejected: registering the
session as `x11` or `wayland` to get the idle hint (it is neither, and
tools would treat it as a local display); the root server talking to
logind itself (it would have to be on the system bus, and D39 keeps D-Bus
in the worker); having connected browsers forward lock requests (browsers
that are not connected would stay unlocked).

**D42 — No "remember this device"; the login screen may remember the user name** (2026-10-23, accepted)
Every login and unlock types the password: there will be no device token,
cookie or stored secret that saves the password or lets a browser skip it.
The login screen gets an opt-in "Remember user name", kept in the
browser's `localStorage` (a user name is not a secret). Chosen by the
user. Replaces the plan in D16 and CLAUDE.md for a revocable server-side
device token. Rejected: device tokens that start or rejoin a session
without the password (a login has to pass PAM to open its session, and
skipping it would widen what a stolen browser profile gets).

**D43 — Two-factor through PAM conversations** (2026-10-23, accepted)
Tilde keeps no second-factor secrets of its own. PAM does the checking
(`pam_google_authenticator`, `pam_oath`, or anything else an administrator
adds to `/etc/pam.d/tilde`), and Tilde passes PAM's extra questions to the
browser: the helper answers the first hidden prompt with the password from
the request, and any later prompt goes to the login screen with PAM's own
words, hidden or shown as PAM asks, while the helper waits on its main
thread. A login in progress is tied to the browser by a short-lived
HttpOnly cookie, times out after about two minutes and counts towards the
rate limit. Unlocking (D40) and logging in to a running session go through
the same conversation, so they ask for the code too. Tilde does not turn
two-factor on: `docs/security.md` shows the line to add, and users set up
their secret with `google-authenticator` in a terminal; an enrollment page
can come later. Logins always start the helper in session mode, and the
server tells it only after authentication whether to open a session or let
the user join the one running: a login can wait minutes for a code, too
long to hold the user's lock. Checks run under a server deadline, not the
request's, and an attempt that reached PAM is never refunded: a browser
that hangs up on slow (wrong) answers would otherwise guess for free.
Answers after the first count as attempts too. Chosen by the user: unlock
asks for the code, two-factor is documented rather than shipped on,
WebAuthn waits.
Rejected for now:
Tilde's own TOTP store (root would hold the secrets, and it would only
cover TOTP); passkeys/WebAuthn as a Tilde-native factor (needs Tilde's own
credential store; maybe later).

**D44 — A polkit authentication agent in the worker; mounting asks for a password** (2026-10-23, accepted)
The worker registers as the polkit authentication agent of its logind
session, only when that is Tilde's own web session (as for D41), so a
program in the session that needs a password (Mount in Files, `pkexec`
or `systemctl` in a terminal) gets Tilde's dialog, as it would get
Cinnamon's on the desktop. The agent never checks a password: like
GNOME's and Cinnamon's, it runs polkit's setuid `polkit-agent-helper-1`
(user name as argument, cookie on stdin), which runs PAM (service
`polkit-1`) and gives polkit the verdict itself. The worker carries the
conversation as the helper does for logins (D43): the password answers
PAM's first hidden prompt, later questions go to the dialog, and a wrong
password leaves the request open for another try. Requests show in every
browser of the user (a `polkit` event, then `GET /api/v1/polkit`), the
first answer ends them, and nobody answering for 5 minutes cancels them.
polkit's `unix-group` identities become the group's members, the user
first when listed. Calls count only from polkit's bus name, since any
user on the system bus can call the agent. No extra rate limit: the user
can already run the helper from a terminal, and PAM's own delays and
`pam_faillock` apply. With the agent, Files mounts volumes (`POST
/api/v1/drives/mount`), the only udisks call that may now wait for a
password; unmounting someone else's mount and powering a drive off still
never ask, as an administrator's password is not worth it there.
Without polkit, its helper or a web session, nothing registers and
mounting fails as before. Rejected: checking passwords in the worker or
through the session helper (polkit accepts verdicts only from its own
helper, as root); a polkit rule letting web sessions mount without a
password (D39); `pkttyagent` per request (a terminal agent, no way to
show its questions in the browser).

**D45 — Security review: headers, proxy, error messages** (2026-10-23, accepted)
A review of the CSP, headers, cookies and error messages (roadmap phase 5)
kept the CSP as it is (`script-src 'self'`; `'unsafe-inline'` only for
styles, which xterm.js and Vue transitions need) and the cookies as they
are (`__Host-` prefix, `HttpOnly`, `Secure`, `SameSite=Strict`, server-side
tokens). It changed: (1) refusals from PAM's auth stack read as a wrong
password, because modules such as `pam_access` answer "permission denied"
before the password is known to be right, which told anyone the account
exists; the account stack, after a right password, still says what
expired. (2) With `--trust-proxy`, the rate limit uses the last
`X-Forwarded-For` address, the one the proxy appended: the first one is
the client's to write, and a new one with every guess escaped the limit.
(3) `Cross-Origin-Resource-Policy: same-origin` on every response.
(4) `Strict-Transport-Security` only with a certificate from `--tls-cert`:
with Tilde's self-signed one it would turn the certificate warning into an
error nobody can click through once the certificate is replaced. (5) The proxy drops
`Set-Cookie` from worker responses and marks them `no-store` unless the
worker chose its own caching, so a listing never outlives a lock in the
browser's cache. Not changed: the Origin check compares `Origin` with
`Host`, which DNS rebinding controls, but the session cookie is bound to
the real host name, so a rebound page can only guess passwords under the
rate limit; a `Host` allowlist would break reaching Tilde by any name the
administrator did not list. The code question of two-factor (D43) shows
that the password was right, as every password-then-code login does.

**D46 — Audit log in the system journal, written by root** (2026-10-23, accepted)
Tilde records who logged in from where and what they did through it:
logins, failed logins and the rate limit; locks (by the user, idle,
logind), unlocks and signing out; sessions starting and ending; terminals
opened and closed; moving to the trash, deleting permanently and emptying
the trash (the first 20 paths and a count); mounts, unmounts and ejects;
polkit authentications (D44), granted or failed. Root writes every record
to the system journal through journald's native protocol, tagged
`tilde-audit`, with the facility authpriv so rsyslog files it in
`/var/log/auth.log` next to sshd's, and with `TILDE_*` fields to filter
on; journald does the rotation, and users cannot change it. The server
records what it sees; the worker reports what the user does on its
session stream (D41), and the server writes it under the session's user,
taking only the event types it knows, at most 200 at once and 20 a second
per user (failed logins from the network share one such budget), and
counting what it drops. The server passes the browser's address to the
worker in `X-Tilde-Client`, so worker events say where an action came
from. Messages escape control characters: file names are the user's to
choose. Commands typed in a terminal are not recorded (auditd does that),
nor are copies, moves and reads. A view of the user's own recent logins
comes with the Settings app (phase 6). Chosen by the user: the journal,
these four groups of events, the viewer later. Rejected: a JSON file of
Tilde's own (rotation to maintain); the worker writing to the journal
itself (records under the user's uid, of the user's making); recording
every file operation (noise, and the user's own business).

**D47 — The panel lists the user's browsers and signs one out** (2026-10-23, accepted)
The tray indicator D16 planned shows the user's signed-in browsers: each
session cookie is one browser (its tabs share it), listed with the
address and `User-Agent` of its login or last unlock, when it logged in,
whether a tab has the desktop open now (its events socket), and whether
it is locked. The number shows once there is more than this browser.
The list comes from the server (`GET /api/auth/clients`), which the
panel asks every 30 seconds, when the page is shown again and when the
list is opened; nothing is pushed, since the server has no channel of its
own to the browser. Each browser has a random id that is not its token.
"Sign Out" on another browser revokes its token, which ends its requests
and sockets, so it shows the login screen; this browser signs out with
Log Out or Switch User, as before. Locked browsers can neither list nor
sign out: the list says where the user is. Signing a browser out is
audited (D46). Why: several browsers may share the session (D12), and one
left open at work should be closable from home without ending the
session. Rejected: showing the count only (a number does not tell which
browser to worry about); pushing changes over the worker's events socket
(the worker does not know the server's tokens, and should not).

**D48 — Text editor: CodeMirror 6, drafts in the worker, saves that keep the file** (2026-10-24, accepted)
The editor is CodeMirror 6: it needs no eval and no web workers, so the
CSP's `script-src 'self'` stays (Monaco wants workers and a looser
policy), and its languages load as chunks of the build. One document per
window, as xed; Files opens text types with it and offers Open With Text
Editor for other files. As D40 requires, unsaved changes live in the
worker: the editor sends a draft half a second after the user stops
typing, and before Lock Screen, to `$XDG_STATE_HOME/tilde/drafts` (0700
folder, 0600 files, named by a hash of the file's path or of an id for a
new document); a window that comes back after a reload or a lock, in any
browser, opens the draft over the file and says if the file changed on
disk meanwhile. Saving and closing without saving delete it. The worker
reads only UTF-8 text without NUL bytes, up to 10 MB. Each read returns a
version (mtime, size, inode); a save names it, and a file changed on disk
since is overwritten only after asking. Saving behaves as GIO's
`g_file_replace` does for xed: a symlink's target is written; a new copy
is renamed into place, with the old permissions and group, unless that
would lose hard links, another user's ownership, a group the user is not
in or an ACL, and then the file is rewritten in place. An unmodified
document follows changes on disk through the folder's inotify events.
Save As took a typed path until the shared file chooser (D49). Not yet:
tabs, reopening an untitled draft whose window is gone (D58). Rejected: Monaco (workers, CSP, size); keeping drafts in the
browser (`localStorage` is per browser and stays readable after a lock);
the upload endpoint for saving (its rename drops permissions and replaces
symlinks).

**D49 — One file chooser for every app, a modal dialog window** (2026-10-24, accepted)
Apps ask for files with `ctx.chooseFile({ mode: 'open' | 'save' |
'folder', filters, multiple, name, folder })`, which resolves to the
chosen paths or null. The chooser follows GTK's, as Cinnamon apps show
it: places, drives and bookmarks on the left, the folder as a path bar,
a sortable list (Files' list view), a filter menu, a name entry when
saving, a location entry for typed paths (Ctrl+L, or just typing), Ctrl+H
for hidden files, Create Folder when saving or picking a folder. It asks
before replacing a file and says so in its answer, so the app saves with
force; a file created after it looked still comes back as a conflict. It
opens as a real window, a modal dialog of the app that asked
(`openDialog` in the window store), not as a page-wide overlay: the rest
of the desktop stays usable, as with GTK's transient dialogs. The parent's
content is `inert`, raising the parent raises the dialog, minimizing or
closing it takes the dialog along, and closing the parent waits for the
dialog. Dialogs are left out of the window list and the saved layout: the
promise behind one does not survive a reload, so it closes then (the
editor's text is safe in its draft). The chooser starts in the folder the
app names, else where the last one was answered, else home. It lists only
what the worker lists, as the user. The text editor uses it for Open
(Ctrl+O, several files, each in a window; an untouched new document gives
its place to the first) and Save As. Its Ctrl+S, Ctrl+Shift+S and Ctrl+O
are handled on the window, not as a CodeMirror keymap, which can take
Ctrl+Shift+S for Ctrl+S. Rejected: the browser's own `<input
type=file>` and download prompts (they see the browser's machine, not the
host); a page-wide modal like `ctx.confirm` (it would block every other
window while a user browses folders).

**D50 — Image viewer: the browser draws, the worker serves** (2026-10-24, accepted)
The image viewer is xviewer's in a window: Files and the desktop open
PNG, JPEG, GIF, WebP, AVIF, BMP, ICO and SVG with it; other image types
(TIFF, RAW, HEIC) still open in a browser tab as before, since the
browser cannot draw them. The `<img>` loads `fs/content`, which the
worker already serves inline with a sandbox CSP and `Last-Modified`, and
an SVG in an `<img>` never runs script; the URL carries the mtime, so a
changed file reloads and an unchanged one comes from the cache. The
viewer steps through the folder's viewable images in Files' name order,
wrapping around (hidden ones only when a hidden one is shown), fetches
the next one ahead, and follows the folder's inotify events: a file
replaced on disk reloads, one deleted moves on to its neighbour. Best Fit
never enlarges a small image; the wheel zooms around the pointer and a
picture bigger than the window is dragged. Rotation turns only the view,
as xviewer does until you save; writing rotated files waits for a reason
to decode and re-encode images in the worker. Delete moves the file to
the trash without asking, as in Files. Fullscreen is the browser's, for
the window's content; F5 starts a five-second slideshow that any key
ends. The window keeps its file in the layout, and the layout now saves a
change still waiting for its debounce when the page unloads: a reload
right after a change used to lose it (and a test of the Backgrounds
window failed now and then because the window came back only sometimes).
Rejected: decoding in the worker for every type (cost and attack surface
for formats few people view in a browser); a canvas renderer (the
browser's `<img>` already handles EXIF orientation, animation and
scaling).

**D51 — Media player: one `<video>` element, the folder as the playlist** (2026-10-25, accepted)
One Media Player plays sound and video, as Celluloid does on Mint; Files
opens `audio/*` and `video/*` with it, except playlist types (`.m3u`,
`.pls`), which are text and open in the editor. A single `<video>`
element plays both; with no picture it shows the file's icon and name.
It loads `fs/content`, whose Range support lets it seek; nothing is
transcoded, so what plays is what the browser decodes (MP4/H.264, WebM,
Ogg, MP3, FLAC, WAV in Chrome and Firefox), and anything else gets a
plain "this browser cannot play" message. The playlist is the folder's
media files in name order (the folder code is now shared with the image
viewer, `lib/folderSiblings.ts`); at the end of a file the next one
plays, and the list stops at its end. The position is saved with the
window every few seconds and on pause, so a reload or a lock (which
clears the page, D40) brings it back where it was, paused: only a
window opened from Files starts playing by itself. Media keys and the
browser's media controls work through the Media Session API, for the
window that played last. The volume is kept per browser in
`localStorage`. Not yet: subtitles, choosing audio tracks, playback
speed. Rejected: transcoding in the worker (CPU, and a decoder for
untrusted files in the user's session for formats most browsers play);
the browser's built-in controls (they cannot follow the theme or show
the playlist).

**D52 — Edge tiling and the window menu, as in Cinnamon** (2026-10-27, accepted)
Dragging a window by its title bar to the left, right or top edge of the
work area tiles it to that half; the ends of those edges (a quarter of
the edge, at most 160 px) give the four quarters. The top edge tiles
rather than maximizes (Cinnamon offers either, as `tile-maximize`; chosen
by the project owner): maximizing already has the button, a double-click
and the menu, and a top half would otherwise be out of reach. The bottom edge is the panel, so the
bottom half waits for keyboard tiling. A preview in the selection color shows
under the window while the pointer is in a zone, and the pointer may be
past the edge (over the panel, or outside the browser while the drag holds
the pointer). The window keeps its own rect: the tile is a field next to
it (`tile`) and is drawn from the work area, so it follows a browser
resize, comes back after a reload, after minimizing and after
maximizing; dragging the window out gives it its own size again under the
pointer, as dragging out of maximized does. Resizing a tile (a handle,
or the menu's Resize) makes it a plain window where it is, rather than
resizing its neighbour, which Muffin does and Tilde leaves for later.
Right-clicking the title bar opens Cinnamon's window menu with the items
that mean something in a browser: Minimize, Maximize or Unmaximize, Move,
Resize, Always on Top and Close. Move and Resize follow the pointer with
no button held, or the arrow keys (10 px); a click or Enter keeps the
change, Escape or a right click undoes it. Browsers cannot warp the
pointer, so it moves the window by how far it goes rather than carrying
it. Always on Top (`above`, saved with the layout) puts the window, and a
dialog of it, in a layer above the others; the window layer is now its own
stacking context, so neither that layer nor z-indexes that grow with every
focus change can reach the panel, the notices or the start menu (before,
about 50 000 focus changes would have put windows over the panel).
Not yet: keyboard tiling (Super+arrows, with the keyboard shortcuts item),
Alt+Space for the menu, workspaces. Rejected: interact.js for the snap
math (D6 kept it as an option; the zones are a few lines and testable on
their own in `wm/snap.ts`); moving the window under the pointer as
Cinnamon's Move does by warping it.

**D53 — Keyboard shortcuts: Cinnamon's keys in fullscreen, Alt+Shift for Super elsewhere** (2026-10-27, accepted)
Most of Cinnamon's shortcuts never reach a web page: the operating system
the browser runs on takes Alt+Tab, Alt+F4 and Super (and a Mint client's
own Cinnamon takes Ctrl+Alt+T and Super+arrows), and the browser keeps
Ctrl+W, Ctrl+T and Ctrl+Tab. So Tilde has both. A Fullscreen button (the
panel and the start menu) puts the whole desktop in fullscreen and, in
Chromium, turns on Keyboard Lock: every key then comes to Tilde, Cinnamon's
own shortcuts work, terminals get Ctrl+W, and holding Escape leaves. In
any browser window, and in Firefox and Safari, which have no Keyboard
Lock, alternates work, with Alt+Shift standing in for Super:

| Action | Cinnamon | Alternate |
| --- | --- | --- |
| Menu | Super (fullscreen only) | Alt+F1 |
| Switch windows | Alt+Tab, Alt+Shift+Tab | Alt+\`, Alt+Shift+\` |
| Tile, maximize, restore | Super+arrows | Alt+Shift+arrows |
| Show desktop | Super+D | Alt+Shift+D |
| Close window | Alt+F4 | Alt+Shift+F4 |
| Window menu | Alt+Space | Alt+Shift+Space |
| Move, resize, maximize | Alt+F7, Alt+F8, Alt+F10 | the window menu |
| Terminal, lock | Ctrl+Alt+T, Ctrl+Alt+L | |

Cinnamon's keys work outside fullscreen too whenever they get through.
Super alone opens the menu only in Keyboard Lock fullscreen: elsewhere the
client's own menu would open as well. Keys are matched by physical key
(`code`), so they sit in the same place on every layout, and AltGr (which
Windows reports as Ctrl+Alt) never counts as Ctrl+Alt. On a Mac the Meta
key is Command, which apps use (Cmd+arrows move the cursor), so Super
combinations are off there and the alternates remain. The shortcuts are
caught on the window before any app sees them, as Cinnamon grabs them
before an X client does: the text editor loses CodeMirror's Alt+Shift+Up
and Down (copy line), and a Mac's Option+Shift+arrows (select by word)
and Option+\` (a dead key) are taken too. Super+arrows push the window
through a grid of halves and quarters as Muffin does (Left then Up is the
top-left quarter, Right from the left half is a plain window again); Up
from a plain window or the top half maximizes and Down from maximized
goes back, so the bottom half is Down from a plain window. Alt+Tab lists
the windows by last use, minimized ones included, and picks one when Alt
is let go. Show Desktop minimizes every window and the next press brings
back those it hid, unless a window came back in between. A window
showing its own content fullscreen (the image viewer, the media player)
now leaves only its own fullscreen, not the desktop's. Not yet: changing
the shortcuts, workspaces (Ctrl+Alt+arrows). Rejected: a leader key (every
shortcut two steps); Ctrl+Alt for Super (a Mint client takes Ctrl+Alt+
arrows for its workspaces and Ctrl+Alt+T); Keyboard Lock without
fullscreen (browsers allow it only in fullscreen).

**D54 — Calendar popup on the clock, drawn by hand** (2026-10-27, accepted)
Clicking the panel clock opens Cinnamon's calendar: today's weekday and
date on top, then a month with buttons and the mouse wheel to page
through months; clicking the month's name comes back to today's. Days of
the neighbouring months fill six weeks, dimmed, so the popup never
changes size. Like the clock, it uses the browser's locale and time zone,
and the week starts on the locale's first day (`Intl.Locale` week info,
or CLDR's Sunday and Saturday regions where the browser has none, as
Firefox). Today moves on at midnight while the popup is open. Not yet:
week numbers (off by default in Cinnamon), events (Cinnamon reads them
from Evolution's data server), "Date and Time Settings" (waits for the
Settings app). Rejected: Reka UI's Calendar (a date picker, with
selection and a dependency on `@internationalized/date`, for a grid that
only shows a month; its first weekday is not taken from the locale
either).

**D55 — Network applet over NetworkManager; the sound applet waits for Xpra** (2026-10-28, accepted)
The panel's network applet shows the host's wired and Wi-Fi devices from
NetworkManager on the system bus, as the user, like drives over udisks
(D39): their state, connection and addresses, the Wi-Fi switch, and the
networks in range by strength, with a lock for secured ones. Clicking a
network connects: a saved connection is used; a new one is made
system-wide with the password in NetworkManager's keeping, as nm-applet
does; a new secured network asks for its password in a Tilde dialog, and
a refused password asks again (the new connection is deleted, so it is
not saved with a password that does not work). The request waits until
the connection is up or has failed; why it failed comes from the device's
`StateChanged` signal, since the device goes from failed to disconnected
at once (and may autoconnect elsewhere), so its `StateReason` property
has forgotten by the time anyone reads it. Disconnect and Wi-Fi off ask first
when the browser may be using that connection: the device holding the
address in the URL, or, for a host name, the one with the default route;
nothing for a browser on the host itself. polkit decides every change
and, from a web session, usually asks for an administrator's password
through Tilde's agent (D44): the worker sets D-Bus's
interactive-authorization flag on its calls while its agent runs.
(Correction, D62: godbus dropped that flag until D62, so NetworkManager
was letting polkit ask by itself.) Without NetworkManager (servers on
systemd-networkd) the applet is hidden. Connections, disconnections and
the switch go to the audit log (D46). Only WPA/WPA2 and WPA3 Personal and
open networks are set up here: enterprise, WEP and Enhanced Open need a
connections editor, as do hidden networks, VPNs and static addresses.
The sound applet moves to Phase 7: a web session is rarely the one the
sound card belongs to (logind gives it to the session at the machine),
so host volume would usually have no device to act on; it means
something once Xpra brings remote apps' sound to the browser. The e2e VM
tests the applet against real Wi-Fi: mac80211_hwsim radios, hostapd and
dnsmasq serving a WPA2 network and an open one, and NetworkManager
managing one more radio only, so the VM's own network is untouched.
Rejected: `nmcli` from the worker (parsing its output, and a process per
request, for what one D-Bus call answers); a NetworkManager secret agent
in the worker, so NetworkManager could ask Tilde for passwords
(heavier, and only needed for the cases left to a connections editor).

**D56 — Bundle the Ubuntu font, with fallbacks that take its space** (2026-10-28, accepted)
Linux Mint draws its interface in Ubuntu 10, title bars in Ubuntu Medium
10 and monospace in DejaVu Sans Mono 10, but the font comes from the
machine the browser runs on: Windows and macOS have no Ubuntu font, and
their Segoe UI or Helvetica take different widths, so labels, title bars
and the Files grid (fixed cell sizes) laid out differently. Tilde now
bundles the Ubuntu font as one WOFF2 file (154 KiB): the variable font
from the fonts-ubuntu package, pinned to normal width with weights 300
to 700, subset to Latin, Greek and Cyrillic. That is a Modified Version
under the Ubuntu Font Licence 1.0, which must keep the original name with
"derivative X" added, so the file is named "Ubuntu derivative Tilde"; its
copyright notice stays in the file and the licence sits next to it. CSS
calls it `Tilde UI`, with one face per weight Tilde uses (400, 500, 700),
each trying the installed Ubuntu first and the bundled file after (a
single face over an installed variable font is unreliable: some browsers
fake the other weights from the regular one). The file goes through Vite
into the hashed, cached `/assets/`, and the server names its type itself
(Go's own table has no `.woff2`). While it loads, fallbacks take
Ubuntu's space: Arial (Windows, macOS) and its metric twins Liberation
Sans and Arimo (Linux), then DejaVu Sans, scaled with `size-adjust` from
average letter widths weighted by English letter frequencies (as Next.js
and fontaine do) and given Ubuntu's ascent and descent, so nothing moves
when it arrives (within about 2% of Ubuntu's width; Chromium on Linux
hints web fonts to whole-pixel letter widths, which makes `size-adjust`
move in steps there, so the test measures with hinting off). `make fonts` (`scripts/build-fonts.py`, fontTools) builds
the font and the `@font-face` rules reproducibly; both are committed, as
the icons are. Title bars now use Medium (500) rather than bold, and
monospace puts DejaVu Sans Mono first, as Mint does. Not bundled:
monospace (Menlo or Consolas elsewhere; the terminal and editor are less
tied to layout), italics (Tilde uses none; browsers slant the regular).
Rejected: shipping the original TTF unchanged (1 MB, plus the width axis
nobody uses); serving the host's installed font from the server (a new
kind of file the server reads, and nothing for hosts without it);
fallbacks alone (the layout would match but not the look).

**D57 — Window animations as transforms, Cinnamon's traditional effect** (2026-10-28, accepted)
Minimizing shrinks a window into its window-list button while it fades
(a dialog into its parent's), and unminimizing grows it back out, in
200 ms; maximizing, restoring and tiling (by dragging or keys) move the
window from where it was in 160 ms, and a drop into a tile starts from
where the pointer let go. The frame takes its new geometry at once and a
Web Animations transform plays the old box into the new one, scaling the
content on the way, as Muffin scales a window's texture: animating the
geometry itself would lay out the content on every frame, and resize a
terminal's PTY as many times. A minimizing frame stays drawn (and takes
no clicks) until it has played. Drags change a window's state on their
own (out of maximized or a tile) and are not animated; neither is
anything under `prefers-reduced-motion`. Opening grows a window from 92%
as it fades in (150 ms); closing shrinks and fades it out (120 ms), the
window taking no clicks or focus meanwhile, through a `TransitionGroup`
that keeps the frame drawn until it has played. The store marks the
windows opened and closed in this page, so windows a reload or an unlock
brings back, and the ones logout or a lock clears, come and go at once.
Not yet: a switch to turn effects off (waits for the Settings app). Rejected: CSS transitions on position and
size (the relayout above); a library (the Web Animations API does it).

**D58 — Recover unsaved documents from a list in the editor** (2026-10-28, accepted)
The worker keeps every unsaved document as a draft (D48), but a draft is
found again only by its window: one whose window is gone (closed in
another browser, a layout lost with the browser's storage) was out of
reach, and for a document never saved there was no other way back. The
worker now lists its drafts without their text (`GET /api/v1/drafts`);
an empty new editor window says when some are not shown by any editor
window in this browser ("2 unsaved documents can be recovered"), and
Show lists them over the editor, newest first: a file's name and folder,
or a never-saved document's first line, and when it was last edited.
Open brings each back in its own window, still unsaved (a file's draft
by opening the file, which loads it); Discard asks first, then deletes
it. An empty window that recovered something closes once the list does,
as one gives its place to opened files. Apps still reach other windows
only through `ctx`: `ctx.appWindows()` gives the saved state of the same
app's other windows. A draft open in another browser's window is listed
too, since the worker cannot know; opening it there as well is the same
as opening one file twice. Rejected: reopening such windows by itself
(windows would appear in every browser that loads the layout, and
drafts a user meant to drop would keep coming back).

**D59 — System Monitor: every user's processes from /proc, signals as the user** (2026-10-28, accepted)
A System Monitor app as GNOME's that Mint ships, starting with its
Processes tab: every process `/proc` shows the user, which is all of them
unless `/proc` is mounted with `hidepid` (the kernel's call), with All
Processes the default view and My Processes and Active Processes beside
it, a search over names, users, PIDs and command lines, and sortable
columns: name, user, CPU (a share of all CPUs since the last list, as
GNOME divides it), PID, resident memory and priority. The worker reads
`/proc/<pid>/stat`, `cmdline` and the directory's owner on each list;
the app lists every three seconds while the page is visible and the
worker keeps one sample, so two browsers a moment apart share it rather
than measure a sliver of time. Stop, Continue, End and Kill are sent as
the user, so the kernel lets them reach only the user's own processes;
the menu offers them for those only, End and Kill ask first, and ending
the worker itself says it ends the session. A signal names the process
by PID and start time, so a PID that ended and came back as another
process is never signalled. Not yet: services (systemd units, a second
tab: list, start, stop and restart, polkit for system ones), Change
Priority, Properties and open files, the Resources and File Systems
tabs (with the system monitor applet). Rejected: streaming changes over
the events socket (a full list every three seconds is simple and small,
and only while the window is open); signalling by PID alone.

**D60 — Services tab: the system's and the user's systemd, start, stop and restart** (2026-10-28, accepted)
System Monitor has a second tab, Services, as Cockpit's: the system's
services from systemd on the system bus, and the user's own from their
user manager on the session bus (pam_systemd's address, or
`$XDG_RUNTIME_DIR/bus`), with a switch between them, a search, and each
service's state and whether it starts at boot. The list merges the
loaded units with the unit files, so services not loaded now show as
inactive; templates (`getty@.service`) are left out, since they cannot
be started as they are. Start, Stop and Restart wait until the service
has settled and say if it failed. systemd and polkit decide: a system
service's calls carry D-Bus's interactive-authorization flag while
Tilde's polkit agent runs, so polkit can ask for an administrator's
password in Tilde's dialog; the user's own manager asks nobody. Stopping
or restarting `tilde.service` asks first, as it ends every session.
Actions on system services go to the audit log (D46), with the state the
service settled in; the user's own are like their own processes and are
not. Each tab of System Monitor lists only while shown: processes every
three seconds, services every five. Not yet: enabling and disabling at
boot, masking, a unit's journal (`journalctl -u`, which journald limits
to what the user may read), timers. Rejected: `systemctl` from the
worker (parsing its output for what two D-Bus calls answer).

**D61 — System monitor applet and Resources tab: one sampler, whole snapshots while watched** (2026-10-28, accepted)
The panel has a system monitor applet: CPU and memory over the last
minute as two small graphs, the figures in its tooltip; a click opens
System Monitor's new Resources tab, as GNOME's: CPU (and each CPU's
share), memory and swap, network receiving and sending, disk reading
and writing, each a graph of the last minute with the figures now in its
legend, and a hover crosshair that reads every series out at a second.
The worker reads `/proc/stat`, `meminfo`, `net/dev` and `diskstats`
(whole disks only: partitions, loop, RAM, zram, device-mapper and md
devices are counted in their disks) once a second, but only while some
browser asks, over the events socket; it sends each asking browser the
whole snapshot, never a change against an earlier one, as the lesson
from webdesktopd says, and keeps the last minute, so a graph opened
later starts full. The applet and the tab each hold a watch while shown;
the first one asks the worker, the last one stops it, and a reconnected
socket asks again. The graphs use two categorical slots (blue, orange,
the dark steps of the dataviz reference palette), checked with its
validator against the view and panel surfaces, and put memory and swap
on one percentage axis, never two scales. Not yet: hiding the applet
(waits for panel settings), File Systems tab, per-interface and per-disk
graphs. Rejected: each browser polling once a second (the applet shows
all day, and every browser would sample on its own); changes instead of
snapshots.

**D62 — A service's journal and its boot setting, in a details pane** (2026-10-28, accepted)
Selecting a service in System Monitor's Services tab shows a pane under
the list: its state, a Start at Boot switch, and its last 200 journal
lines, errors in red, reloaded when the service changes state (not on
each refresh of the list). A reader at the end follows new lines; one
who has scrolled up keeps the line they were reading at the top, found
again by its time and text as older lines drop off. The
journal comes from `journalctl -u <service> -o json` (`--user` for the
user's own services), run as the user with the name as its own argument
and only for a service the manager lists, so journald decides what the
user may read: their own logs, and the system's only in `adm` or
`systemd-journal`; when it held some back the pane says so rather than
looking empty. The switch calls systemd's `EnableUnitFiles` and
`DisableUnitFiles`, for services with an `[Install]` section (the pane
says why the others cannot be switched: static, masked, generated...);
system ones ask polkit, and both go to the audit log. Unlike `systemctl
enable`, systemd is not reloaded afterwards: the setting is on disk and
the next boot reads it, and a reload is a second administrator's
password from a web session. Here systemd lets polkit ask only when the
call carries D-Bus's interactive-authorization flag (starting and
stopping always may), and godbus's `Object.Call` drops that flag: it
keeps only NoAutoStart and NoReplyExpected. The worker now builds such
calls itself (`dbuscall.go`) for systemd and NetworkManager, and uses
`EnableUnitFilesWithFlags`, as systemctl does. Not yet: masking, following the journal
live, timers. Rejected: reading the journal files directly (journalctl
already applies journald's access rules and formats).

**D63 — Editor tabs, as xed: files open in the last editor window** (2026-10-28, accepted)
The text editor's windows hold tabs, one document each with its own
draft (D48). A file opened from Files, the desktop or Open… becomes a tab
of the most recently used editor window, as in xed, switching to it if
it is open already; an empty tab nobody touched gives its place. The
shell routes it: an app whose manifest sets `reuseWindow` gets launches
with a `path` in its last window's `ctx.onLaunch` handler instead of a
new window (`newWindow: true` asks for one anyway); launched without a
path (the menu, New Window), it opens a window. The tab bar always
shows, as in xed, so a single document's tab can be dragged too; tabs
show an unsaved dot and a close button, close with the middle button,
reorder by dragging, move into another editor window when dropped on it
(its tab bar, or anywhere in it; a window left without tabs closes; the
tab travels in the page, its unsaved changes written to its draft as the
drag starts, and the new window waits for that before opening it), and
move to a window of their own from their menu or when dropped outside
any editor window; the desktop and Files take only paths
and files to upload, so a tab dropped on them goes to its own window
too, and they do not light up for it. Text files dragged from Files or
the desktop into the editor open as tabs (what CodeMirror would paste is
their path); other files are named as not text, and files dragged in
from the user's computer must be uploaded to a folder first. Each tab keeps its own CodeMirror
while hidden, so undo survives switching. Closing a tab with unsaved
changes asks; closing the window asks once for all of them. The window
keeps its tabs and the active one in its saved state; windows from
before tabs (one document as path and draft) come back as one tab. The
browser keeps Ctrl+N, Ctrl+W and Ctrl+Tab outside fullscreen (D53), so
the keys are xed's where they arrive (Ctrl+PgUp and Ctrl+PgDn always;
Ctrl+N, Ctrl+W, Ctrl+Tab in Keyboard Lock fullscreen) plus Ctrl+Alt+N and
Ctrl+Alt+W, and Alt+1 to Alt+9 pick a tab. Rejected: hiding the tab bar's
reorder and move behind menus only (xed drags them); keeping one window
per file (the user chose xed's way).

**D64 — Hosts: systemd Linux, the Debian family first, Raspberry Pi included** (2026-10-28, accepted; the Pi run is on a real Pi, D74)
Tilde needs Linux with systemd and logind (pam_systemd makes the web
session, its runtime folder and session bus; the Services tab is
systemd's), PAM, and a D-Bus system bus; polkit, udisks2 and
NetworkManager are used when present and not needed. Nothing needs
Cinnamon or Mint: every desktop setting Tilde reads (the lock delay, the
background, Nemo's icon layout) has a fallback, and the theme, icons and
font are bundled. The targets are the Debian family first: Ubuntu
(desktop and headless server, which the e2e VM is), Raspberry Pi OS,
Linux Mint and Pop!_OS; then other systemd distributions such as Arch
(CachyOS) and Fedora, whose differences are packaging's (D65): the PAM
file (`@include common-auth` is Debian's; Arch includes `system-login`,
Fedora `system-auth`) and build dependencies. Distributions without
systemd (Alpine, Void, Devuan) are out of scope. The code has nothing
tied to a CPU; the cgo build for PAM must be native or use an ARM C
toolchain and ARM libpam. The requirements are written down in
docs/development.md ("Host requirements"). Raspberry Pi OS gets an e2e
run of its own (arm64, under QEMU), small and only on request, so the
usual run stays as fast as it is: `make e2e-pi`, not part of
`make e2e-vm`.

**D65 — Packages with nfpm, released on GitHub** (2026-10-28, accepted)
Tilde is packaged with nfpm, from one configuration: `.deb` first, for
amd64 and arm64, then `.rpm` and Arch packages, each with its family's
PAM file and the systemd unit and `/etc/default/tilde` as `make install`
puts them. (Settled the same day: no armhf, as the Raspberry Pis are Pi
4s, which run 64-bit Raspberry Pi OS; installing enables and starts
Tilde on localhost, D68.) A release workflow builds them (natively on
ARM runners, as the PAM build needs cgo) and attaches them to a GitHub
release; an apt repository, and the AUR, can follow once people use it.
Rejected: debhelper (Debian packages only, where the targets span
families, D64).

**D66 — Local web apps on a second port** (2026-10-28, accepted)
Local web apps (code-server, Jupyter) are served from a separate origin,
as rule 8 requires, by a second port on the same host, which the main
server listens on and proxies to the app, with the same TLS
certificate. Wildcard subdomains were the other way, but they need DNS
and a wildcard certificate, which a Raspberry Pi or a VM on a home
network rarely has (D64); a port works by IP address or `.local` name
alike. Browsers keep cookies per host, not per port, and SameSite counts
both ports as one site, so: the app port strips Tilde's cookies before
proxying, its own authentication uses a cookie of its own name, set by a
one-time handoff from the desktop, and every state-changing request and
WebSocket upgrade on the main port keeps its Origin check (rule 6),
which turns away the app's pages, whose origin has the other port.

**D67 — Tilde's settings live in `~/.config/tilde`** (2026-10-28, accepted)
The Settings app saves Tilde's settings under `~/.config/tilde/`,
written by the worker as the user, as the desktop layout and background
already are (D36, D37). A desktop's own settings (Cinnamon's, GNOME's)
are read as defaults where they exist and never written: most hosts
have neither (D64), and changing a setting in a browser should not
change the desktop at the machine. The idle lock delay follows the
desktop's when it has one and is 15 minutes otherwise, unless set in
Tilde (D40). Rejected: writing Cinnamon's gsettings (nothing to write to
on most hosts, and a surprise on the others).

**D68 — Reaching Tilde: localhost by default, the install says how to open it** (2026-10-28, accepted; amends D24)
The package enables and starts Tilde, listening on `127.0.0.1:8443` as
D24 has it, and prints how to reach it from elsewhere: from the LAN or a
VPN (WireGuard, Tailscale) with `--listen` on that address, through an
SSH tunnel (`ssh -L 8443:127.0.0.1:8443 host`), or from the internet
behind a reverse proxy with its own certificate (`--trust-proxy`,
`--allowed-origins`), and with two-factor (D43). The user reaches it
from the LAN for now and wants to try it from outside soon, by SSH
tunnel or over the internet. Each of those works with the server as it
is; listening on more than one address at once (localhost and the LAN)
and getting a certificate by itself (ACME) would be additions.

**D69 — Sessions last at most 12 hours, a server setting** (2026-10-28, accepted; settles D16's open question)
A session ends 12 hours after login, however active, and the user logs
in again; the user wants no more than that and does not mind logging in.
The limit becomes a setting of the server's (in `/etc/default/tilde`),
an administrator's policy rather than a user's preference; idle locking
(D40) stays separate.

**D70 — Tilde is the notification service when nothing else is** (2026-10-28, accepted; replaces the open "Events" question)
`notify-send` and other programs' notifications show as Tilde's toasts:
the worker takes the `org.freedesktop.Notifications` name on the user's
session bus when no other program has it, as on a headless server or a
Pi. While a desktop at the machine has it (Cinnamon's own notifications),
Tilde leaves it alone; one user's sessions share one session bus, so
only one program can have the name. Showing notifications in both
places at once is still open (see Open decisions).

**D71 — Remote apps with Xpra** (2026-10-28, accepted; confirms D26)
Host graphical apps run in Tilde windows through Xpra's HTML5 client; the
first ones the user has in mind are VS Code and Disk Usage Analyzer,
which Xpra's per-window forwarding suits. Streaming games (Steam), which
needs another approach, is a separate question (see Open decisions).

**D72 — System Settings: lock, effects, panel, recent logins** (2026-10-28, accepted)
A System Settings app with Cinnamon's modules Tilde has a use for: the
screen lock (follow the desktop's delay, Cinnamon's or GNOME's as D40
reads it, or Tilde's own: on or off and the minutes), window animations
(D57), which panel applets show, a link to Backgrounds, and recent
logins. Changes apply at once, as in Cinnamon, and are kept by the worker
in `~/.config/tilde/settings.json` (D67); a file from an older Tilde
keeps the defaults for what it lacks, and the desktop's settings are only
read. Saving sends every browser a `settings` event and the server the
new lock delay at once, rather than at the next minute's re-read. Recent
logins come from the audit log (D46), which only root may read, so the
server serves them, filtered to the session's own user and the login
events, and to entries the server itself wrote (`_UID`, which journald
sets): anyone may send the journal an entry tagged `tilde-audit`. The
worker could not read the log. The theme waits until there is a
second one.

**D73 — Packages: built on Debian 12, installed in `/usr/bin`, tested in the VM** (2026-10-28, accepted; carries out D65, D68, D69)
The release workflow builds each package natively (GitHub's amd64 and
arm64 runners) in a Debian 12 container: a cgo binary needs the glibc it
was built against or newer, and Debian 12's (2.36) is the oldest of the
targets (D64), Raspberry Pi OS bookworm included; one built on Ubuntu
24.04 would not start there. The package installs `/usr/bin/tilde` and
the unit in `/usr/lib/systemd/system`; `make install`, outside the
package manager, keeps `/usr/local`. Its scripts follow debhelper's:
enable and start on a new install unless the administrator disabled it,
restart on an upgrade (which ends the sessions running; an upgrade is
the administrator's choice of when), mask on removal, delete
`/var/lib/tilde` on purge. The e2e VM installs the `.deb` instead of
copying files, so every run tests the package. The session lifetime
(D69) is `--session-lifetime` in `/etc/default/tilde`, 10 minutes to a
week; the bounds are there to catch a mistyped value, the user's own
limit being 12 hours (D69).

**D74 — The Raspberry Pi tests run on a spare Pi, not under QEMU** (2026-10-28, accepted; amends D64)
`make e2e-pi PI=user@host` runs a small part of the suite on a real
Raspberry Pi over SSH, which the user has (Pi 4s), instead of an arm64 VM:
Raspberry Pi OS's kernel is built for the Pi's hardware and does not boot
on QEMU's generic `virt` machine, so a VM would test either a plain Debian
arm64 image (Pi OS's base, not Pi OS) or Pi OS with another kernel put in
(fragile), and emulated, slowly. A real Pi tests Pi OS's own kernel,
packages and hardware, natively and fast. It must be one that can be
wiped, as the e2e VM is (D29): the script refuses a Pi without
`/etc/tilde-e2e-disposable`. Tilde is built on the Pi (cgo for PAM is
native there) and packaged here, and the Pi installs the `.deb` with apt.
Rejected: both QEMU ways above.

---

## Open decisions

| Question | Options | Leaning | Needed by |
| --- | --- | --- | --- |
| Notifications in both places | mirror the desktop's notifications into Tilde by watching the session bus (D70) | view-only mirror, a setting | When notifications are built |
| Game streaming | WebRTC video from the host GPU (capture, hardware encode, input), alongside Xpra (D71) | explore after Xpra | Phase 7 |
| Multi-machine | third-party tunnel vs own relay; E2E encryption | worker over SSH first | Phase 8 |
