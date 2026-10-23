# Lessons from webdesktopd

[eramax/webdesktopd](https://github.com/eramax/webdesktopd) is a
browser-based Linux desktop in Go and SvelteKit, built in three days
(38 commits, March 27–29, 2026, mostly by AI agents) and dormant since. It
has no license file, so Tilde borrows ideas, never code. Studied on
2026-10-20.

## What it does

- SSH credential check against local sshd, then a 24-hour JWT.
- One WebSocket with a custom binary frame protocol (about 21 frame types)
  for terminals, file transfer, stats and port proxying.
- Terminals on PTYs with ring-buffer replay; a file manager; a `/proc` stats
  dock; a proxy for local web apps with port auto-discovery.
- Three apps toggled by visibility; no real window manager.

## What it confirmed

- Per-user sessions with ring-buffer terminal replay are the right core.
  Reconnect took about six rounds of fixes there (for example, a disconnect
  closed the PTYs), so Tilde designed it up front: stable terminal ids,
  replay plus forced redraw, and an explicit `session_ended` code instead of
  retrying forever.
- A custom frame protocol is expensive: hand-chunked 64 KB uploads,
  client-computed progress, ever more frame types. Tilde's HTTP-over-yamux
  gets uploads, downloads, Range and WebSockets from the standard library.
- SSH-only auth gives no PAM session (no logind, D-Bus, keyring, polkit),
  requires sshd, and means pasting private keys into a browser. Tilde uses
  PAM.

## Gaps Tilde fills

1. **File operations ran in the daemon, not as the user.** Only PTYs dropped
   privileges; under OpenRC the daemon ran as root, so any user had root-level
   file access. Tilde does everything in a worker running as the user.
2. **Proxied apps shared the desktop's origin**, with a script-readable
   cookie and stripped `X-Frame-Options`/`frame-ancestors`, so any local web
   app could drive the user's terminal. Tilde: local web apps on a separate
   origin; HttpOnly cookies.
3. **"Remember me" stored the password or private key in IndexedDB.** Tilde
   never stores passwords in the browser.
4. **Stateless JWTs** with no revocation, sent in the URL; WebSocket accepted
   any origin. Tilde: server-side revocable tokens in cookies; Origin checks.
5. **No TLS, listened on all interfaces**, no login rate limit. Tilde:
   self-signed TLS by default, localhost by default, rate limiting.
6. No Cinnamon shell, window manager, trash spec, MIME associations or app
   model.

## What changed in Tilde's plan because of it

- **No systemd mount sandboxing on the unit.** Its unit used
  `ProtectSystem=strict` and `ProtectHome=read-only`; child processes inherit
  those, so users' shells could not write outside `/home` (breaking things
  like `sudo apt install`). Tilde's unit has none, and pam_systemd moves each
  session into its own scope.
- **Local web apps** (port discovery from `/proc/net/tcp`, open code-server
  or Jupyter in a window) are on the roadmap, served from a separate origin.
- **No "remember me"** that keeps the password: the login screen may remember
  the user name only (D42).
- **A system monitor tray applet**: always send full snapshots (a widget that
  mounted late saw `undefined` fields there).
- **Packaging**: it shipped a static `CGO_ENABLED=0` binary; Tilde needs cgo
  for PAM, so the `.deb` depends on `libpam0g`. Its release workflow took seven
  fix commits, so budget time for ours.
- **Real-privilege testing**: its PTY tests were skipped because they needed
  root. Tilde's e2e suite runs with real PAM and setuid in a VM.
