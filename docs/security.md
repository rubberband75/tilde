# Security

Tilde gives full control of a Linux account through a web page. That is the
point, so the job is to make sure only the right person gets it, and that a
bug in Tilde can never give more than the account already has.

## Threat model

| Attacker | Goal | Main defenses |
| --- | --- | --- |
| Someone on the network | log in, or ride a logged-in browser | not exposed by default; TLS; PAM; rate limiting + pam_faillock; Origin checks; SameSite=Strict HttpOnly cookies |
| A malicious website the user visits | act through the user's browser (CSRF, cross-site WebSocket hijacking) | Origin check on every state-changing request and WebSocket upgrade; `SameSite=Strict` |
| A file on the user's disk (HTML, SVG) | run script on Tilde's origin | downloads served with `Content-Security-Policy: sandbox`, `nosniff`, forced attachment for non-media types |
| A bug in Tilde's API code | escape the user's permissions | all user operations run in the worker as the user; the server never touches files |
| Another local user | reach someone else's session | the worker has no network port; sessions keyed by authenticated user; workers run under different UIDs |

## Process boundaries

- **Server (root)**: TLS, PAM-backed login via the helper, tokens, proxying.
  It never opens user files or runs user commands. It never calls setuid.
- **Session helper (root)**: PAM authentication and session. Reads the
  password from a pipe, never from arguments or the environment.
- **Worker (the user)**: everything else. Started with the user's UID, GID
  and supplementary groups and a clean environment. Reachable only over its
  stdin/stdout pipe.

## Implemented

- PAM service `tilde` (`packaging/pam.d/tilde`): `pam_nologin`, the standard
  `common-auth` / `common-account` / `common-session` stacks (so
  `pam_faillock`, 2FA modules and `pam_systemd` apply), `pam_loginuid`,
  `pam_keyinit`, `pam_env`, optional `pam_gnome_keyring`.
- Root login refused before PAM runs; any UID 0 account refused after lookup.
- Unknown users and wrong passwords get the same error.
- Login rate limit: 5 failures per address in 15 minutes, then `429`.
  `X-Forwarded-For` is ignored unless `--trust-proxy`.
- Session tokens: 32 random bytes, stored by SHA-256 hash in memory, cookie
  `__Host-tilde` with `HttpOnly; Secure; SameSite=Strict; Path=/`, 12-hour
  absolute lifetime, all revoked on logout and when the session ends.
- Origin check (`internal/server/middleware.go`): every non-GET/HEAD/OPTIONS
  request and every WebSocket upgrade must carry an `Origin` whose host equals
  the request's `Host` (or one passed with `--allowed-origins`).
- Security headers on every response: strict CSP (`script-src 'self'`),
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`,
  `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`.
- File downloads (`GET /api/v1/fs/content`): `Content-Security-Policy:
  sandbox` (except PDF, whose viewer needs to run), `nosniff`, and
  `Content-Disposition: attachment` for anything that is not an image,
  audio, video, plain text or PDF.
- Uploads write to a temporary file in the target directory, then link or
  rename into place; a cancelled upload leaves nothing behind.
- TLS 1.2+; self-signed certificate created on first start, fingerprint
  logged. `--dev` mode refuses non-loopback addresses.
- Default listen address is `127.0.0.1:8443`.

## Rules (also in CLAUDE.md)

1. No user file or process operations in the server.
2. No setuid/setgid in a Go process; only `SysProcAttr.Credential` on a child.
3. PAM sessions open in the helper, which outlives the session.
4. Helper/worker stdout is the protocol.
5. The worker never listens on a network port.
6. Origin checks on every state-changing request and WebSocket upgrade.
7. Never store passwords in the browser.
8. Files from disk never run script on Tilde's origin; local web apps get a
   separate origin.
9. No systemd sandboxing options on the unit (user processes inherit them).
10. Root login stays disabled.
11. `script-src` stays `'self'`.

## Not yet (see roadmap phase 5)

- Lock screen and idle detection.
- "Remember this device" as a revocable server-side device token.
- TOTP and other multi-step PAM conversations.
- Password re-prompt for admin actions through a polkit agent.
- Audit log of logins, started commands and deletions.

## Deployment advice

- Keep the default localhost binding and reach Tilde over Tailscale or
  WireGuard (`--listen <vpn-address>:8443`). This removes most of the risk.
- If it must face a LAN, use your own certificate (`--tls-cert`,
  `--tls-key`) and enable `pam_faillock` in `common-auth`.
- Do not put it on the public internet without 2FA, and even then prefer a
  VPN.
- Behind a reverse proxy, pass `--allowed-origins https://your.host` and
  `--trust-proxy`.
