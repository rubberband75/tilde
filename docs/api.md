# API

All endpoints are same-origin JSON over HTTPS. State-changing requests and
WebSocket upgrades need an `Origin` header for this host (browsers send it).
Errors look like:

```json
{ "error": "human-readable message", "code": "machine_code" }
```

Common codes: `bad_request`, `not_found`, `permission_denied`, `exists`,
`other_drive`, `not_empty`, `not_a_directory`, `is_a_directory`, `no_space`,
`read_only`, `internal_error`, `not_logged_in`, `session_ended`, `bad_origin`,
`stale`, `busy`, `locked`.

Worker responses are `Cache-Control: no-store` unless the endpoint says
otherwise (thumbnails, file contents), and never set cookies: the server's
proxy enforces both.

TypeScript types for these responses live in `web/src/api/types.ts`; keep
them in sync.

## Auth (main server)

| Method | Path | Body | Response |
| --- | --- | --- | --- |
| POST | `/api/auth/login` | `{username, password}` | `200 SessionInfo` and sets the session cookie, or `200 {prompt}` when PAM asks more (below). `401 auth_failed`, `403 root_login_disabled` / `account_expired` / `password_expired` / `not_allowed`, `429 rate_limited` |
| POST | `/api/auth/answer` | `{answer}` or `{cancel: true}` | Answers the question a login or unlock is waiting on: `200 {prompt}` (another one), or what the login or unlock returns when done. `401 auth_failed` (a wrong answer: start again), `401 auth_expired` (no question waiting, or it took over two minutes), `409 busy`, `204` after cancel |
| POST | `/api/auth/logout` | — | `204`. Ends the user's session on all devices; from a locked browser it only signs that browser out |
| POST | `/api/auth/leave` | — | `204`. Signs this browser out and leaves the session running (the lock screen's Switch User) |
| GET | `/api/auth/session` | — | `200 SessionInfo`, also while locked, or `401 not_logged_in` / `session_ended` |
| POST | `/api/auth/activity` | — | `200 {lockAfter, heartbeat}`: there was keyboard or pointer input. `423 locked` if this browser is locked |
| POST | `/api/auth/lock` | — | `204`. Locks every browser logged in as this user. `423 locked` from a locked browser |
| GET | `/api/auth/clients` | — | `200 {clients: ClientInfo[]}`: the user's signed-in browsers (D47), this one first, then the most recently seen. `ClientInfo`: `{id, address, agent, since, lastSeen, lastInput, locked, connected, current}`; `address` and `agent` (its `User-Agent`) are from its login or last unlock; `connected`: a tab has the events socket open. `423 locked` from a locked browser |
| POST | `/api/auth/clients/signout` | `{id}` | `204`: signs that browser out (its requests and sockets end). `400` for this browser (log out instead), `404 not_found`, `423 locked` |
| GET | `/api/auth/logins` | — | `200 {available, entries: LoginRecord[]}`: this account's last 50 logins and failed attempts (logins, failed logins and unlocks, the rate limit), newest first, read by the server from the audit log (D46, D72). `LoginRecord`: `{time, event, rhost, text, failed}`; `time` in ms, `text` the audit message without the user's name. `available` is false when the journal cannot be read. `423 locked` from a locked browser |
| POST | `/api/auth/apps/handoff` | `{for?}` | `200 {code, origin}`: a one-time code for the app port (D76), or with `for: "xpra"` the Xpra port (D78), and that port's origin. Post the code in a form (`code`, `next`) to `<origin>/_tilde/enter`; it lasts a minute. `404 apps_off` when local web apps are off, `423 locked`, `429 busy` with too many waiting |
| POST | `/api/auth/unlock` | `{password}` | `200 SessionInfo` and a new session cookie (the old one stops working); only this browser unlocks. `401 auth_failed`, `403 account_expired` / `password_expired`, `429 rate_limited` (shared with login: 5 attempts per address in 15 minutes), `401 too_many_attempts` after 5 tries on this cookie, which is then signed out |

`SessionInfo` is `{user, uid, home, locked, lockAfter, heartbeat}`; login
returns it too.

Two-factor (D43): when PAM asks something after the password (a TOTP
module's "Verification code:"), login and unlock answer `200 {"prompt":
{text, echo, info?}}`: PAM's own words, whether the answer may be shown,
and PAM's messages since the last question. A short-lived `HttpOnly` cookie
(`__Host-tilde-auth`) holds the waiting check for this browser; send the
answer to `/api/auth/answer`. The check is the same rate-limited attempt as
the password.

Locking (D40) is per browser: tabs share the cookie, and the server keeps
the lock. A browser locks when `lockAfter` seconds pass without an
`activity` report (`0`: never), or when any of the user's browsers calls
`lock`. While locked, `/api/v1/*` answers `423 locked` and the server
closes the browser's open requests and WebSockets; show the lock screen.
Lock and `423` responses, `session` while locked, logout and leave carry
`Clear-Site-Data: "cache"`.
Report input on the first event after a quiet spell, then at most every
`heartbeat` seconds while it continues; the server allows twice that for
reports on their way. `lockAfter` is Tilde's own delay if the user set
one in System Settings (D72), else follows the user's Cinnamon screensaver
settings (`idle-delay` + `lock-delay`, when `idle-activation-enabled` and
`lock-enabled`), else GNOME's, else 900. The token lives 12 hours from
login or the last unlock (`tilde serve --session-lifetime`, D69).

## Worker API (`/api/v1/*`)

Proxied unchanged to the user's worker. Without a valid session:
`401 not_logged_in`, or `401 session_ended` if the token was valid but the
session is gone (the frontend returns to login). `423 locked` while this
browser is locked. `502 worker_unavailable` if the worker stops responding.
The server sets `X-Tilde-Client` to the browser's address on every request
it proxies (replacing any the browser sent), for the audit log (D46).

The worker also serves two routes to the server only: the server proxies
nothing outside `/api/v1/`, and they need the `X-Tilde-Internal` header,
which the proxy strips.

- `GET /internal/session`: NDJSON, `{"type":"settings", source, lockAfter}`
  on connect and when the lock settings change (D40), `{"type":"lock"}`
  when logind asks the session to lock (D41), and `{"type":"audit",
  audit: {event, rhost?, paths?, count?, terminal?, name?, device?,
  action?, identity?, result?, dropped?}}` for the audit log (D46).
  Lines are at most 4 KiB.
- `PUT /internal/session/locked` `{locked}` → `204`: sets the logind
  session's `LockedHint`; `502 logind` if logind refuses.

Paths in query strings and bodies are absolute.

### Session and system

| Method | Path | Response |
| --- | --- | --- |
| GET | `/api/v1/hello` | `{user, uid, home, shell, hostname, version, protocol}` |
| GET | `/api/v1/system` | `{hostname, os, kernel, mounts: Mount[]}` |
| GET | `/api/v1/places` | `{places: [{id, name, path, icon}]}` — Home, XDG user dirs, File System, Trash |
| GET | `/api/v1/bookmarks` | `{bookmarks: [{path, name, exists}]}` from `~/.config/gtk-3.0/bookmarks` (shared with Nemo); only local folders are listed |
| POST | `/api/v1/bookmarks` | `{path, name?}` → `{bookmarks}`. Adds, or renames an existing bookmark. Other lines (`sftp://`…) are kept; URIs are escaped as GLib does |
| DELETE | `/api/v1/bookmarks?path=` | → `{bookmarks}` |
| GET | `/api/v1/desktop/layout` | `{saved, positions: {key: {col, row}}, nemo?}`. Desktop icon cells, from the top left. Keys are file names in the Desktop folder, or `computer:///`, `home:///`, `trash:///`. Until a layout is saved, `saved` is false and `nemo` is Nemo's arrangement to start from: `{grid?: [w, h], icons: {key: {x, y}}}` in screen pixels (GVfs `metadata::nemo-icon-position` read with `gio`, and `~/.config/nemo/desktop-metadata`; icons on the monitor most are on) |
| GET | `/api/v1/desktop/background` | `{source, picture, options, primary, secondary, shading}`: Tilde's own background if one was picked (`source: "tilde"`, `~/.config/tilde/desktop-background.json`), else Cinnamon's (`"cinnamon"`, `org.cinnamon.desktop.background` read with `gsettings`), else `"default"` (the theme's). `picture` is a path (empty: colors only); `options` is `none`, `wallpaper` (tiled), `centered`, `scaled`, `stretched`, `zoom` or `spanned`; colors are `#rrggbb`; `shading` is `solid`, `vertical` or `horizontal` |
| PUT | `/api/v1/desktop/background` | The same fields without `source` → the background now shown. Unknown options and bad colors fall back to defaults; `picture` must be absolute. `{reset: true}` drops Tilde's background. Cinnamon's settings are never written |
| GET | `/api/v1/desktop/backgrounds` | `{collections: [{name, images: Entry[]}]}`: Cinnamon's wallpaper lists (`cinnamon-background-properties/*.xml` in `$XDG_DATA_DIRS`), the Pictures folder, and folders from `~/.cinnamon/backgrounds/user-folders.lst`; images only, top level, at most 1000 each |
| GET | `/api/v1/settings` | `{settings: Settings, desktopLock: {source, lockAfter}, desktopTheme: {source, mode, accent}}`. `Settings` (D72), from `~/.config/tilde/settings.json`, defaults for anything missing: `{lock: {own, enabled, minutes}, appearance: {mode, accent}, effects: {animations}, panel: {resources, network, notifications, fullscreen}}`. `appearance` (D77): `mode` `dark`, `light`, `mixed` or `desktop`; `accent` `green`, `aqua`, `blue`, `grey`, `orange`, `pink`, `purple`, `red`, `sand`, `teal` or `desktop`. `lock.own`: Tilde's own delay (`enabled`, `minutes` 1–1440) rather than the desktop's. `desktopLock` is what the desktop says (`source`: `cinnamon`, `gnome` or `default`; `lockAfter` in seconds, 0 never); `desktopTheme` `{source, mode, accent}` likewise, from Cinnamon's GTK and desktop themes or GNOME's colour scheme and accent |
| PUT | `/api/v1/settings` | A whole `Settings` → the saved settings; `400` for a delay out of range. Every client gets a `settings` event, and the server the new lock delay at once. The desktop's settings are never written |
| POST | `/api/v1/desktop/layout` | `{positions: {key: {col, row} \| null}, replace?}` → layout. Merges (`null` removes a key) or, with `replace`, starts from nothing. Stored in `~/.config/tilde/desktop-layout.json`; cells up to 1000, at most 5000 icons |

### Files

`Entry`:

```json
{
  "name": "notes.txt", "path": "/home/chandler/notes.txt",
  "type": "file",            // "dir" | "file" | "other"
  "symlink": false, "target": "", "broken": false,
  "size": 11, "mode": "-rw-r--r--", "perm": 420, "uid": 1000, "gid": 1000,
  "mtime": "2026-10-20T13:02:54Z", "mime": "text/plain", "hidden": false,
  "thumb": false             // a thumbnail can be made
}
```

`mime` comes from the name (shared-mime-info globs). Files the name says
nothing about are sniffed: ELF binaries (`application/x-executable`,
`application/x-sharedlib`), `#!` scripts by interpreter, empty files
(`application/x-zerosize`), then text, HTML, images, PDF and archives by
content. A listing sniffs at most 1000 files; the rest fall back to
`application/x-executable` (executable bit) or `application/octet-stream`.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/fs/list?path=` | `{path, parent, entries: Entry[]}`, folders first |
| GET | `/api/v1/fs/list?path=&stream=1` | NDJSON (`application/x-ndjson`), for huge folders: `{path, parent}`, then `{entries: Entry[]}` lines of up to 1000 unsorted entries, then `{done: true, total}`; `{error, code}` if reading fails midway. Errors before the first line are normal JSON errors |
| GET | `/api/v1/fs/search?path=&q=[&hidden=1]` | NDJSON like a streamed listing: entries below `path` whose names contain every word of `q` (case-insensitive), then `{done, total, truncated}`. Hidden items only with `hidden=1`; links are not followed; stays on `path`'s filesystem; at most 5000 results |
| GET | `/api/v1/fs/properties?path=` | `{entry, owner, group, atime, ctime, mine, free, capacity}`: names of owner and group, whether the user owns it, free space and size of its filesystem |
| GET | `/api/v1/fs/usage?path=[&path=…]` | NDJSON progress about every 250 ms: `{items, size, unreadable, done}`, counting everything below the paths (like `du -x`: one filesystem, links not followed). Close the request to stop |
| POST | `/api/v1/fs/chmod` | `{path, perm}` (0–0777) → `Entry`. Keeps setuid/setgid/sticky; refuses links; the kernel allows it only for the owner |
| GET | `/api/v1/fs/stat?path=` | `Entry` (lstat; symlinks report their target's type) |
| GET | `/api/v1/fs/thumbnail?path=&size=normal\|large[&v=mtime]` | PNG thumbnail (128 or 256 px) from the freedesktop cache (`~/.cache/thumbnails`, shared with Nemo), made on demand by the host's thumbnailers or, for PNG/JPEG/GIF, a built-in scaler. `404 no_thumbnail` if none can be made (failures are cached per the spec). Only for entries with `thumb: true` |
| GET | `/api/v1/fs/content?path=[&download=1][&v=]` | File bytes; supports `Range`. Sandboxed CSP; non-media types are forced to download. `v` is ignored: the UI puts the mtime there so a changed file gets a new URL |
| PUT | `/api/v1/fs/content?path=[&overwrite=1]` | Body is the file. `201 Entry`; `409 exists` without overwrite |
| POST | `/api/v1/fs/mkdir` | `{path}` → `201 Entry` |
| POST | `/api/v1/fs/rename` | `{from, to, overwrite?}` → `Entry`. Same filesystem only (`409 other_drive` otherwise); without `overwrite` an existing `to` gives `409 exists`, atomically |
| POST | `/api/v1/fs/trash` | `{paths: []}` → `{results: [{path, name?, error?, code?}]}` |

### Drives (udisks2, D39)

The worker asks udisks on the system bus, as the user; udisks and polkit
decide what is allowed. Without a system bus or udisks, the list comes from
the mount table and `udisks` is false.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/drives` | `{udisks, volumes: Volume[]}`: the root first, then mounted volumes, then unmounted ones. `Volume`: `{id, device, name, fsType, size, free?, mountPoint?, removable, canEject, system, icons}`. `id` is a udisks block object path, or `mount:` and the mount point for mounts udisks does not know. `name` is the label, else the mount folder's name, else "4.0 GB Volume". `canEject`: mounted under the user's `/media` (what udisks mounted for them), or on a removable non-system drive. `icons` are freedesktop names, most specific first |
| POST | `/api/v1/drives/mount` | `{id}` → `Volume`, mounted (at once if it already is). From a web session polkit wants a password: it asks through Tilde's agent (D44), and the request waits while the dialog is open. `403 cancelled` if the user cancels, `403 not_authorized` if polkit refuses (or no agent is running) |
| POST | `/api/v1/drives/unmount` | `{id}` → `204`. udisks lets users unmount their own mounts; others need an administrator: `403 not_authorized`. `409 busy` while files are open on it |
| GET | `/api/v1/processes` | `ProcessList` (D59): `{processes: Process[], uid, self, cpus}`: every process `/proc` shows the user, by PID. `Process`: `{pid, ppid, name, command?, uid, user, state, cpu, memory, nice, threads, start, kernel?}`. `cpu`: percent of all CPUs since the previous list (lists less than a second apart share one sample); `memory`: resident bytes; `start`: start time in clock ticks after boot, which with the PID names one process; `self`: the worker's PID |
| POST | `/api/v1/processes/signal` | `{pid, start, signal: TERM\|KILL\|STOP\|CONT\|HUP\|INT}` → `204`, sent as the user. `404 gone` if the process ended or its PID now belongs to another (another `start`); `403 permission_denied` for another user's process |
| GET | `/api/v1/services?scope=` | `{available, services: Service[]}` (D60), `scope` `system` (the system's systemd) or `user` (the user's own manager). `Service`: `{name, description?, load?, active, sub, boot?}`: systemd's load, active and sub states, and the unit file's state (`enabled`, `disabled`, `static`, `masked`...). Unit files not loaded now are listed as `inactive`/`dead`; templates (`getty@.service`) are left out. `available` is false without that manager |
| POST | `/api/v1/services/action` | `{scope, name, action: start\|stop\|restart}` → `{active, sub}` once the service has settled (up to 30 s, plus a polkit dialog). System services ask polkit, which from a web session usually wants an administrator's password through Tilde's agent (D44): `403 not_authorized` if refused. The user's own services ask nobody. `400` for a service not listed |
| GET | `/api/v1/services/journal?scope=&name=` | `{entries: JournalEntry[], limited}` (D62): the service's last 200 journal lines, oldest first, read by `journalctl -u` (`--user` for the user's own) as the user. `JournalEntry`: `{time, priority, ident?, pid?, message}` (`time` in Unix milliseconds, `priority` syslog's). `limited`: journald showed only what the user may read (not in `adm` or `systemd-journal`). `400` for a service the manager does not list |
| POST | `/api/v1/services/boot` | `{scope, name, enabled}` → `{boot}`, the unit file's state after enabling or disabling it at boot. System services ask polkit (`403 not_authorized`). systemd is not reloaded afterwards |
| GET | `/api/v1/network` | `NetworkState` (D55): `{available, wifiEnabled, wifiHardware, devices: NetDevice[]}`. `available` is false without NetworkManager. `NetDevice`: `{id, interface, type: ethernet\|wifi, state: connected\|connecting\|disconnected\|unavailable\|deactivating\|failed, connection?, addresses?, default, networks?}`; only wired and Wi-Fi devices NetworkManager manages, wired first. `default`: carries the default route. `networks` (Wi-Fi): `{ssid, strength (0–100), security: none\|psk\|sae\|enterprise\|other, active, known}`, one per network name (its strongest access point), the connected one first, then by strength; hidden networks left out |
| POST | `/api/v1/network/wifi` | `{enabled}` → `204`. The Wi-Fi switch; polkit may ask for a password through Tilde's agent (D44): `403 not_authorized` if refused |
| POST | `/api/v1/network/scan` | `{device}` → `204`. Asks a Wi-Fi device to scan now; results come as `network` events. A scan polkit refuses is not an error (NetworkManager scans by itself) |
| POST | `/api/v1/network/connect` | `{device, ssid?, password?}` → `{connected, reason: ""\|wrong_password\|failed\|timeout}` once the connection is up or has failed (up to 45 s, plus a polkit dialog). Wired: NetworkManager picks the connection. Wi-Fi: `ssid` must be in range; a saved connection is used (with `password`, its password is replaced first), else a new system-wide one is made. `400 password_required` for a new secured network without `password`, `400 unsupported` for enterprise and other security. A new connection whose password was refused is deleted again |
| POST | `/api/v1/network/disconnect` | `{device}` → `204`. NetworkManager leaves the device down until it is connected again |
| POST | `/api/v1/drives/eject` | `{id}` → `{unmounted, ejected}`. Unmounts every mounted volume of the drive, then powers the drive off (or ejects the medium). polkit refuses that last step outside an active local session, so `ejected` is usually false from a browser; the drive is unmounted and safe to unplug either way |

Only mounting asks polkit for a password (through the agent below);
unmount and eject never wait for one.

### polkit agent (D44)

The worker is the polkit authentication agent of Tilde's web session.
When a program in the session needs a password (Mount above, `pkexec` in
a terminal), the request shows up here and the events socket sends
`{"type":"polkit"}`; every browser of the user shows the dialog, and the
first answer ends it. Unanswered requests are cancelled after 5 minutes.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/polkit` | `{agent, requests: PolkitRequest[]}`, oldest first. `agent`: registered with polkit. `PolkitRequest`: `{id, action, message, icon?, identities, prompt?}`. `identities` are the users whose password polkit accepts, the user first when listed; `prompt` (`{text, echo, info?}`, as at login) is a question PAM asked after the password, waiting for its answer |
| POST | `/api/v1/polkit/respond` | `{id, identity?, answer}`. With no `prompt` waiting, `answer` is the password of `identity` (default: the first). `204` authorized (the request ends), `200 {prompt}` another question, `401 auth_failed` wrong (the request stays open; `error` carries PAM's message if it gave one), `404 not_found` the request ended, `409 busy` an answer is being checked. Answers: at most 511 bytes, no line breaks |
| POST | `/api/v1/polkit/cancel` | `{id}` → `204`; the program that asked is told it was dismissed. `404` if it already ended |

### Local web apps (D76)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/apps` | `{apps: LocalApp[]}`, by port: the TCP ports the user's own programs listen on (`/proc/net/tcp`, `tcp6`). `LocalApp`: `{port, address, process?, pid?, http, title?}`; `http`: it answered `GET /` within a second, `title` its page's `<title>` |

The app port (`--apps-listen`, by default the port after `--listen`'s;
browsers reach it on the port as far from the one they reached Tilde at,
or at `--apps-origin`) serves:

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/_tilde/enter` | Form `code`, `next`. The code from `/api/auth/apps/handoff`, once, from the origin that asked for it (or, as Tilde sends no Referer, `Origin: null` with `Sec-Fetch-Site: same-site`); `next` is `/proxy/<port>/…` or `/absproxy/<port>/…`. Sets the app cookie (`__Host-tilde-app`, `HttpOnly`, `SameSite=Strict`) and redirects (`303`) to `next`. `401` expired or spent, `403` from another origin, `423` locked |
| any | `/proxy/<port>/…` | The user's app on `<port>`, the prefix taken off (apps with relative links, such as code-server). `X-Forwarded-Prefix` names it |
| any | `/absproxy/<port>/…` | The same, the path kept (apps told their base path, such as Jupyter with `--ServerApp.base_url=/absproxy/<port>/`) |

The Xpra port (`--xpra-listen`, by default the port after the app port's;
browsers reach it two ports from Tilde's, or at `--xpra-origin`) serves
`/_tilde/enter` likewise (its codes are `for: "xpra"`, its cookie
`__Host-tilde-xpra`, `next` under `/xpra/`) and `/xpra/…`: the user's Xpra
session, its HTML5 client and WebSocket, the prefix taken off; `502` when
the session is not running. On both ports a WebSocket is taken only from
the port's own origin.

Requests reach the app with the browser's `Host`, `X-Forwarded-*`, and its
cookies but Tilde's. Its responses cannot set Tilde's cookies, and may be
framed by the desktop that opened them only (`frame-ancestors`; the app's
own `X-Frame-Options` and `frame-ancestors` are dropped). WebSockets pass.
Without a valid app cookie: `401`; locked: `423`; nothing of the user's
on the port: `502`; each a small page.

### Host apps (D78)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/host-apps` | `{available, apps: HostApp[]}`, by name: the host's `.desktop` apps (`$XDG_DATA_HOME` first, then `$XDG_DATA_DIRS`; hidden, `Terminal=true`, `OnlyShowIn` and missing `TryExec` ones left out). `HostApp`: `{id, name, comment?, icon?, iconPath?, category}`; `id` is the desktop file ID, `iconPath` the icon found in hicolor or pixmaps, `category` the menu's. `available` is false without Xpra and xpra-html5 (or a runtime folder), and the list is then empty |
| POST | `/api/v1/host-apps/launch` | `{id}` → `204`: the app's `Exec` (arguments with field codes dropped) runs as argv, without a shell, on the user's Xpra session's display, the session started first if need be (on `$XDG_RUNTIME_DIR/tilde/xpra.sock`). `404 not_found`, `404 no_xpra`, `502 launch_failed` |
| POST | `/api/v1/host-apps/session` | → `204`: starts the session, for the Host Apps window. `404 no_xpra`, `502 start_failed` |

The session is shown by Xpra's HTML5 client at `/xpra/index.html` on the
Xpra port (after a handoff `for: "xpra"`), which reaches the socket
through the worker; only these two endpoints start it. It ends when the
session does.

### Notifications (D70)

The worker is the notification server (`org.freedesktop.Notifications`,
specification 1.2) on the user's session bus while no other program is:
it waits in the bus's queue while one is (Cinnamon at the machine) and
allows replacement, so a desktop starting later takes the name back.
`notify-send`, apt and other programs' notifications then show in every
browser; the events socket sends `{"type":"notifications"}` whenever the
list changes. It offers `actions`, `body`, `icon-static` and
`persistence`, not `body-markup`: markup sent anyway is removed. The last
50 are kept, in memory; a notification without its own timeout closes
after 6 seconds, a critical one stays until dismissed, and closed ones
stay listed (transient ones do not).

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/notifications` | `{active, notifications: Notification[]}`, newest first. `active`: Tilde has the name. `Notification`: `{id, app, icon?, iconPath?, image?, summary, body?, actions?: [{key, label}], urgency, time, open}`: `icon` an icon name, `iconPath` a picture file, `image` a picture sent with it (below); `urgency` 0 low, 1 normal, 2 critical; `time` in Unix ms; `open`: shown as a toast |
| GET | `/api/v1/notifications/image?id=` | The picture sent with notification `id` (`image-data`, at most 512×512), as a PNG, with a sandbox CSP. `404` if none |
| POST | `/api/v1/notifications/action` | `{id, key}` → `204`: the program gets `ActionInvoked`; the notification then closes (`NotificationClosed`, reason 2) unless it is resident. `"default"` is a click on the notification itself. `404` if it closed or has no such action |
| POST | `/api/v1/notifications/close` | `{id}` → `204`: dismisses the toast (reason 2); it stays listed. `404` if unknown |
| POST | `/api/v1/notifications/clear` | `{ids?}` → `204`: removes those from the list, or all of them, closing any still open |

### Text editor (D48)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/text?path=` | `{text, version, writable, entry}`. `415 not_text` for binary or non-UTF-8 files, `413 too_large` over 10 MB, `400 is_a_directory`. `version` names the contents (mtime, size, inode) |
| PUT | `/api/v1/text?path=&base=[&force=1]` | Body: the text (UTF-8). `base` is the version it was opened at, `""` for a new file. `200 {version, entry}`; `409 changed` if the file changed (or was deleted) since `base`, `409 exists` for a new file whose name is taken; `force=1` saves anyway. A symlink's target is written; a new copy is renamed into place unless that would lose hard links, another owner, the group or an ACL, then the file is rewritten in place. Permissions are kept |
| GET | `/api/v1/drafts?key=` | `Draft`: `{key, text, base?, updated}`, or `404`. Keys: `file:` + path, or `new:` + an id for a document never saved |
| GET | `/api/v1/drafts` | Without `key`: `{drafts: DraftInfo[]}`, every draft newest first, without its text (D58). `DraftInfo`: `{key, base?, updated, size, preview}`; `size` in bytes, `preview` the first line with something on it (at most 80 characters) |
| PUT | `/api/v1/drafts?key=` | `{text, base}` → `204`. Kept in `$XDG_STATE_HOME/tilde/drafts` (folder 0700, files 0600) |
| DELETE | `/api/v1/drafts?key=` | `204`, also when there was none |

### Trash (freedesktop Trash spec, D38)

Files on the home filesystem go to `~/.local/share/Trash`. Files on another
drive (a mount from the Computer list) go to that drive's trash:
`$topdir/.Trash/$uid` if the administrator made a sticky `$topdir/.Trash`,
else `$topdir/.Trash-$uid` (created 0700). Trash folders that are symlinks
or not the user's are never used. Items on other mounts (tmpfs, read-only
drives) cannot be trashed: `POST /api/v1/fs/trash` reports `other_drive`.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/trash` | `{dir, filesDirs, items: [{name, originalPath, deletedAt, entry}]}`, newest first, from every trash. `dir` is the home trash; `filesDirs` are the `files/` folders of all trashes (watch them). `name` is the item's name in the home trash, or the absolute path of the item inside a drive's trash |
| POST | `/api/v1/trash/restore` | `{names: []}` → `{restored: [paths]}`; recreates missing parent folders |
| POST | `/api/v1/trash/empty` | `204` |

### File operation jobs (D31)

Copy, move and delete run in the background as jobs. Progress reaches every
client on the events WebSocket.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/jobs` | `{op, sources: [], dest?}` → `202 Job`. `op`: `copy`, `move`, `drop` (move within a filesystem, copy across, as Nemo's drag and drop), `delete` (permanent; no `dest`) |
| GET | `/api/v1/jobs` | `{jobs: Job[]}`, oldest first. Finished jobs stay until dismissed, at most an hour |
| GET | `/api/v1/jobs/{id}` | `Job` |
| POST | `/api/v1/jobs/{id}/cancel` | `204`. Files already copied stay; a partly copied file is removed |
| POST | `/api/v1/jobs/{id}/resolve` | `{conflict, action, name?, applyToAll}` → `204`; `409 stale` if that conflict was already answered (another client), `400` if the action is not in `conflict.actions` |
| DELETE | `/api/v1/jobs/{id}` | Dismiss a finished job → `204`; `409 busy` while it runs |

`Job`:

```json
{
  "id": "j3", "op": "copy", "sources": ["/home/me/a"], "dest": "/media/usb",
  "state": "running",   // preparing | running | waiting | done | failed | cancelled
  "current": "/home/me/a/b.txt",
  "totalItems": 120, "doneItems": 30, "totalBytes": 52428800, "doneBytes": 1048576,
  "conflict": null,     // set while waiting, see below
  "errors": [{"path": "...", "error": "...", "code": "permission_denied"}],
  "errorCount": 1,      // errors keeps the first 100
  "error": "",          // why a failed job could not run at all
  "started": "...", "finished": null
}
```

`conflict`: `{id, source: Entry, dest: Entry, suggested, actions}`. `actions` is
`replace, skip, rename` for files, `merge, skip, rename` for two folders, and
`skip, rename` when a file would replace a folder. `rename` takes `name`
(`suggested` is free). `applyToAll` repeats the answer for later conflicts of
the same kind (file or folder) in this job.

Behavior: links are copied as links, never followed; special files are
skipped with an error; permission bits and modification times are kept (not
setuid/setgid or ownership). Copying into the item's own folder makes
"name (copy)". Moving onto another filesystem copies, then deletes each
source item once it is copied. Errors on single items do not stop the job;
they are listed when it ends. Deleting an item in `Trash/files` also removes
its `.trashinfo`. Logging out cancels running jobs.

### Events (WebSocket)

`GET (WS) /api/v1/events`: JSON text frames from the worker. The first is
`{"type":"snapshot","jobs":[...]}`; then `{"type":"job","job":{...}}` whenever
a job changes (progress at most every 150 ms) and `{"type":"job-removed","id"}`.
Events carry whole objects, so a client never has to merge partial updates.
A client that falls too far behind is closed with 1013 and should reconnect.

`{"type":"drives"}` says drives or volumes changed (plugged in, mounted,
unmounted, from udisks signals, at most every 300 ms once a client has
asked for `/api/v1/drives`): fetch the list again.

`{"type":"network"}` says NetworkManager's devices, connections or access
points changed (at most once a second once a client has asked for
`/api/v1/network`): fetch it again.

`{"type":"resources","on":true}` from the client asks for resource
snapshots (D61), `on:false` stops them. While some client asks, the worker
samples `/proc` once a second and sends each asking client
`{"type":"resources","resources":Resources}`, always a whole snapshot;
right after asking, `{"type":"resources-history","history":[...]}` with the
last minute (up to 60, oldest first). `Resources`: `{time, cpu, cores[],
memory: {total, used, cache?}, swap: {total, used}, netReceive, netSend,
netReceived, netSent, diskRead, diskWrite}`: percentages of CPU since the
previous snapshot, bytes, and bytes a second (network over every interface
but loopback, disk over whole disks). A reconnected socket starts
unwatched: ask again.

`{"type":"settings"}` says the user saved System Settings in some
browser (D72): fetch `/api/v1/settings` again.

`{"type":"notifications"}` says programs' notifications changed (D70):
fetch `/api/v1/notifications` again, and after each snapshot too.

`{"type":"polkit"}` says polkit's authentication requests changed (D44):
fetch `/api/v1/polkit` again. Requests made while the socket was down are
not repeated, so fetch it after each snapshot too.

Folder changes (D34): the client sends `{"type":"watch","dirs":[...]}` with
every folder it shows (absolute, clean paths; at most 256). Each message
replaces the client's previous set, and resending a folder whose watch was
lost (it was deleted and has come back) watches it again. The worker
watches them with inotify, not recursively, and sends
`{"type":"fs","dirs":[...]}` when entries are added, removed or renamed,
change metadata or finish being written, or the folder itself goes away.
Changes are collected for 200 ms, so a busy folder costs a few events a
second. `fs` events go to every client of the user; ignore folders you do
not show. After reconnecting, send the set again and reload what you show:
changes made while disconnected are not replayed.

### Terminals

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/terminals` | `{terminals: [{id, created, cols, rows, exited}]}` |
| POST | `/api/v1/terminals` | `{cols?, rows?, cwd?}` → `201 TerminalInfo`. Starts the user's login shell |
| DELETE | `/api/v1/terminals/{id}` | Hangs up the shell (SIGHUP, then SIGKILL after 2 s) |
| GET (WS) | `/api/v1/terminals/{id}/attach` | WebSocket. See below |

Terminal WebSocket:

- On connect the worker sends the ring buffer (up to 1 MiB of recent output)
  as binary frames, then live output. Clear the screen before writing.
- Client → worker binary frames are keyboard input.
- Client → worker text frames are JSON control messages:
  `{"type":"resize","cols":120,"rows":32}`. The first resize after attaching
  forces a redraw.
- Close reasons: `exited:<code>` when the shell ends (close the window);
  status 1013 `too slow` when the client fell behind (reconnect).
- Several clients can attach to the same terminal.

## Planned

- Notification events on `/api/v1/events`.
