# API

All endpoints are same-origin JSON over HTTPS. State-changing requests and
WebSocket upgrades need an `Origin` header for this host (browsers send it).
Errors look like:

```json
{ "error": "human-readable message", "code": "machine_code" }
```

Common codes: `bad_request`, `not_found`, `permission_denied`, `exists`,
`other_drive`, `not_empty`, `not_a_directory`, `is_a_directory`, `no_space`,
`read_only`, `internal_error`, `not_logged_in`, `session_ended`, `bad_origin`.

TypeScript types for these responses live in `web/src/api/types.ts`; keep
them in sync.

## Auth (main server)

| Method | Path | Body | Response |
| --- | --- | --- | --- |
| POST | `/api/auth/login` | `{username, password}` | `200 {user, uid, home}` and sets the session cookie. `401 auth_failed`, `403 root_login_disabled` / `account_expired` / `password_expired` / `not_allowed`, `429 rate_limited` |
| POST | `/api/auth/logout` | — | `204`. Ends the user's session on all devices |
| GET | `/api/auth/session` | — | `200 {user, uid, home}`, or `401 not_logged_in` / `session_ended` |

## Worker API (`/api/v1/*`)

Proxied unchanged to the user's worker. Without a valid session:
`401 not_logged_in`, or `401 session_ended` if the token was valid but the
session is gone (the frontend returns to login). `502 worker_unavailable` if
the worker stops responding.

Paths in query strings and bodies are absolute.

### Session and system

| Method | Path | Response |
| --- | --- | --- |
| GET | `/api/v1/hello` | `{user, uid, home, shell, hostname, version, protocol}` |
| GET | `/api/v1/system` | `{hostname, os, kernel, mounts: Mount[]}` |
| GET | `/api/v1/places` | `{places: [{id, name, path, icon}]}` — Home, XDG user dirs, File System, Trash |

### Files

`Entry`:

```json
{
  "name": "notes.txt", "path": "/home/chandler/notes.txt",
  "type": "file",            // "dir" | "file" | "other"
  "symlink": false, "target": "", "broken": false,
  "size": 11, "mode": "-rw-r--r--", "perm": 420, "uid": 1000, "gid": 1000,
  "mtime": "2026-10-20T13:02:54Z", "mime": "text/plain", "hidden": false
}
```

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/fs/list?path=` | `{path, parent, entries: Entry[]}`, folders first |
| GET | `/api/v1/fs/stat?path=` | `Entry` (lstat; symlinks report their target's type) |
| GET | `/api/v1/fs/content?path=[&download=1]` | File bytes; supports `Range`. Sandboxed CSP; non-media types are forced to download |
| PUT | `/api/v1/fs/content?path=[&overwrite=1]` | Body is the file. `201 Entry`; `409 exists` without overwrite |
| POST | `/api/v1/fs/mkdir` | `{path}` → `201 Entry` |
| POST | `/api/v1/fs/rename` | `{from, to, overwrite?}` → `Entry`. Same filesystem only (`409 other_drive` otherwise) |
| POST | `/api/v1/fs/trash` | `{paths: []}` → `{results: [{path, name?, error?, code?}]}` |

### Trash (freedesktop home trash, `~/.local/share/Trash`)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/trash` | `{dir, items: [{name, originalPath, deletedAt, entry}]}`, newest first |
| POST | `/api/v1/trash/restore` | `{names: []}` → `{restored: [paths]}`; recreates missing parent folders |
| POST | `/api/v1/trash/empty` | `204` |

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

- `GET (WS) /api/v1/events`: inotify changes, job progress, notifications.
- `POST /api/v1/jobs` copy/move/delete with progress and conflict prompts.
- `POST /api/auth/unlock` for the lock screen.
