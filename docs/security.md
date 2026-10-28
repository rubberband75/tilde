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
| Someone at an unattended browser | use the open session | idle lock per browser (D40), kept by the server: a locked browser's requests are refused and its sockets closed; unlocking needs the password |
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
- Two-factor through PAM (D43): questions PAM asks after the password go
  to the login and lock screens, held by a 2-minute `HttpOnly` cookie;
  a wrong answer starts over and counts as a failed attempt, as does every
  answer after the first when a module asks again. Checks run under the
  server's own deadline, so hanging up early never undoes a wrong guess,
  and an attempt is only taken back on cancel before any answer or on a
  verdict that says nothing about the secret (an expired account). At most
  2 checks wait per user, 4 per address and 64 in all. Off by default; see
  "Two-factor" below.
- The worker's hello (user and uid) must match the account that logged in
  and the uid the root helper started: the worker is the user's process,
  and tokens are issued for the user it names.
- Unknown users and wrong passwords get the same error. So does any other
  refusal from PAM's auth stack (`pam_access`, `pam_sss` saying "permission
  denied" or "expired"), since the password is not known to be right yet;
  only the account stack, after a right password, may say an account or
  password has expired.
- Rate limit for logins and unlocks together: 5 attempts per address in 15
  minutes, then `429`. An attempt counts when it starts, before PAM
  answers, so parallel requests get no more; a right password takes back
  only that user's attempts. `X-Forwarded-For` is ignored unless
  `--trust-proxy`, and then only its last address counts: the one the
  proxy appended, not those the client wrote.
- Session tokens: 32 random bytes, stored by SHA-256 hash in memory, cookie
  `__Host-tilde` with `HttpOnly; Secure; SameSite=Strict; Path=/`, 12-hour
  absolute lifetime from the last time the password was typed (login or
  unlock), all revoked on logout and when the session ends.
- Lock screen (D40): the server locks a browser's token after the user's
  idle delay without input (Cinnamon's screensaver settings) or when any of
  the user's browsers presses Lock Screen. A locked token gets `423` from
  every worker route, and locking ends its open requests and WebSockets.
  Unlocking runs the PAM auth and account stacks through the helper,
  counts towards the login rate limit and replaces the token; after 5
  tries a locked token is revoked, so copying its cookie elsewhere buys no
  more guesses. A locked browser can neither log the session out (it only
  signs itself out) nor lock the user's other browsers. The page drops the
  desktop while locked, and lock, `423`, logout and leave responses send
  `Clear-Site-Data: "cache"`, so file contents the browser cached are gone.
  Not covered yet: files Tilde opened in their own browser tab (types with
  no viewer app) stay open, and the window layout in `localStorage` keeps
  window titles and folder paths.
- The panel lists the user's signed-in browsers (address, `User-Agent`,
  open or locked) and can sign one of the others out (D47), so a session
  left open elsewhere can be closed without logging out everywhere;
  signing one out is in the audit log. Locked browsers can do neither:
  the list would show where the user is.
- The server reads the worker's session stream in lines of at most 4 KiB:
  the worker is the user's process, and must not be able to make the root
  server buffer without limit.
- `loginctl lock-session` locks every browser of the session; logind's
  unlock request is ignored, so only the password unlocks (D41). The worker
  takes a `Lock` signal only from logind's own bus name: any user can send
  a signal straight to another user's worker.
- polkit agent (D44): the worker is the authentication agent of its own
  web session only. It never checks a password: polkit's setuid
  `polkit-agent-helper-1` runs PAM and reports to polkit, and the worker
  passes the password and PAM's questions between it and the dialog
  (answers of at most 511 bytes, no line breaks; never logged). The cookie
  reaches the helper on stdin, not its command line. Only polkit's own bus
  name may start or cancel a request, so another user cannot put a
  password dialog in this user's browser. The browser may answer only as
  one of the users polkit named. Locked browsers cannot reach it (`423`).
- Network (D55): the worker changes the host's network only through
  NetworkManager on the system bus, as the user, so NetworkManager and
  polkit decide (from a web session polkit usually wants an
  administrator's password). Requests can only name devices and networks
  NetworkManager lists. A Wi-Fi password goes to NetworkManager, which
  stores it in its system connection as nm-applet does; the worker keeps
  and logs none, and the browser's field is a password entry with no
  autocomplete. A new connection whose password was refused is deleted.
  Turning off what the browser may be using asks first.
- Services (D60): the worker starts, stops and restarts systemd services
  only over D-Bus, as the user: systemd asks polkit for the system's
  (usually an administrator's password, through Tilde's agent), and the
  user's own manager is theirs. Requests can only name services the
  manager lists. Stopping `tilde.service` asks first. A service's journal
  is read by `journalctl` as the user, the name passed as its own
  argument, so journald's own access rules apply (D62).
- Audit log (D46): root writes to the system journal, tagged
  `tilde-audit` with the facility authpriv (so rsyslog also files it in
  `/var/log/auth.log`): logins and failed logins, the rate limit, locks
  (by the user, idle or logind), unlocks, signing out, sessions starting
  and ending, terminals, deletions (move to trash, delete permanently,
  empty trash; the first 20 paths and a count), mounts, unmounts, ejects,
  polkit authentications, network connections, disconnections, the
  Wi-Fi switch, and starting, stopping, restarting, enabling and
  disabling system services.
  Fields: `TILDE_EVENT`, `TILDE_USER`,
  `TILDE_RHOST`, and per event `TILDE_PATH`, `TILDE_COUNT`,
  `TILDE_REASON`, `TILDE_ACTION`... Read it with `journalctl -t
  tilde-audit _UID=0` (`TILDE_USER=alice` to filter): any process may
  send the journal an entry tagged `tilde-audit`, but `_UID` is
  journald's own, so `_UID=0` keeps only root's. The worker reports its
  events to the server, which writes them under the session's user,
  accepts only the event types it knows, and takes at most 200 at once
  and 20 a second from each user; failed logins from the network share
  one such budget. Messages escape control characters, so a file name
  cannot forge a line in `auth.log`. Commands typed in a terminal are not
  recorded: use auditd for that.
  System Settings shows a user their own recent logins and failed
  attempts (D72): the server, as root, runs `journalctl -t tilde-audit`
  matching `_UID` to its own (so no other process's forged entries),
  `TILDE_USER` to the session's user and only the login events, so a user
  sees their own records and none of anyone else's. It reads at most 1 MiB
  of four fields, with at most four reads running at once. A locked
  browser cannot ask. The worker cannot read the audit log: it runs as the
  user, who usually may not read the system journal.
- Origin check (`internal/server/middleware.go`): every non-GET/HEAD/OPTIONS
  request and every WebSocket upgrade must carry an `Origin` whose host equals
  the request's `Host` (or one passed with `--allowed-origins`).
- Security headers on every response: strict CSP (`script-src 'self'`),
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`,
  `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`,
  `Cross-Origin-Resource-Policy: same-origin`, and a `Permissions-Policy`
  without camera, microphone or location. `Strict-Transport-Security`
  (one year) only with a certificate passed with `--tls-cert` (D45).
- Worker responses pass through the server's proxy, which drops any
  `Set-Cookie` (cookies are the server's; the worker is the user's
  process) and marks them `Cache-Control: no-store` unless the worker set
  its own caching (thumbnails, file contents). Server JSON is always
  `no-store`.
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
3. PAM sessions open in the helper, which outlives the session, from its
   main thread (pam_loginuid fails from any other thread).
4. Helper/worker stdout is the protocol.
5. The worker never listens on a network port.
6. Origin checks on every state-changing request and WebSocket upgrade.
7. Never store passwords in the browser.
8. Files from disk never run script on Tilde's origin; local web apps get a
   separate origin.
9. No systemd sandboxing options on the unit (user processes inherit them).
10. Root login stays disabled.
11. `script-src` stays `'self'`.

## Two-factor

Tilde asks whatever PAM asks, so two-factor is a PAM module in
`/etc/pam.d/tilde` (D43). For TOTP codes from an authenticator app:

1. `sudo apt install libpam-google-authenticator`
2. Each user runs `google-authenticator` once, in a terminal (a Tilde
   terminal works), and scans the QR code.
3. Uncomment the line after `@include common-auth` in `/etc/pam.d/tilde`:

   ```
   auth       required   pam_google_authenticator.so nullok
   ```

   `nullok` lets accounts that have not set up a code log in with the
   password alone; drop it once everyone has.

Put the module after `@include common-auth`: Tilde answers PAM's first
hidden prompt with the password, and a wrong password then fails before the
code is asked. That order means the code question itself tells whoever
typed the password that it was right, as with any password-then-code
login; the rate limit still applies to both steps. Unlocking a locked screen asks for a code too. `pam_oath`
works the same way (the e2e VM tests with it).

## Deployment advice

- Keep the default localhost binding and reach Tilde over Tailscale or
  WireGuard (`--listen <vpn-address>:8443`). This removes most of the risk.
- If it must face a LAN, use your own certificate (`--tls-cert`,
  `--tls-key`) and enable `pam_faillock` in `common-auth`.
- Do not put it on the public internet without two-factor (above), and even
  then prefer a VPN.
- Behind a reverse proxy, pass `--allowed-origins https://your.host` and
  `--trust-proxy`. The proxy must append the client's address to
  `X-Forwarded-For` (nginx `$proxy_add_x_forwarded_for`, Caddy and
  Traefik by default); with more than one proxy, the rate limit sees the
  last one's client.
- The Origin check compares the `Origin` header with `Host`, which a DNS
  rebinding attack controls too. Such a page still never gets the session
  cookie (it is bound to the real host name), so all it can do is guess
  passwords, under the rate limit; a certificate from `--tls-cert` makes
  the browser refuse the rebound name as well.
