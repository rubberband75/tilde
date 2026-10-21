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
`stale`, `busy`.

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
| GET | `/api/v1/bookmarks` | `{bookmarks: [{path, name, exists}]}` from `~/.config/gtk-3.0/bookmarks` (shared with Nemo); only local folders are listed |
| POST | `/api/v1/bookmarks` | `{path, name?}` → `{bookmarks}`. Adds, or renames an existing bookmark. Other lines (`sftp://`…) are kept; URIs are escaped as GLib does |
| DELETE | `/api/v1/bookmarks?path=` | → `{bookmarks}` |
| GET | `/api/v1/desktop/layout` | `{saved, positions: {key: {col, row}}, nemo?}`. Desktop icon cells, from the top left. Keys are file names in the Desktop folder, or `computer:///`, `home:///`, `trash:///`. Until a layout is saved, `saved` is false and `nemo` is Nemo's arrangement to start from: `{grid?: [w, h], icons: {key: {x, y}}}` in screen pixels (GVfs `metadata::nemo-icon-position` read with `gio`, and `~/.config/nemo/desktop-metadata`; icons on the monitor most are on) |
| GET | `/api/v1/desktop/background` | `{source, picture, options, primary, secondary, shading}`: Tilde's own background if one was picked (`source: "tilde"`, `~/.config/tilde/desktop-background.json`), else Cinnamon's (`"cinnamon"`, `org.cinnamon.desktop.background` read with `gsettings`), else `"default"` (the theme's). `picture` is a path (empty: colors only); `options` is `none`, `wallpaper` (tiled), `centered`, `scaled`, `stretched`, `zoom` or `spanned`; colors are `#rrggbb`; `shading` is `solid`, `vertical` or `horizontal` |
| PUT | `/api/v1/desktop/background` | The same fields without `source` → the background now shown. Unknown options and bad colors fall back to defaults; `picture` must be absolute. `{reset: true}` drops Tilde's background. Cinnamon's settings are never written |
| GET | `/api/v1/desktop/backgrounds` | `{collections: [{name, images: Entry[]}]}`: Cinnamon's wallpaper lists (`cinnamon-background-properties/*.xml` in `$XDG_DATA_DIRS`), the Pictures folder, and folders from `~/.cinnamon/backgrounds/user-folders.lst`; images only, top level, at most 1000 each |
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
| GET | `/api/v1/fs/content?path=[&download=1]` | File bytes; supports `Range`. Sandboxed CSP; non-media types are forced to download |
| PUT | `/api/v1/fs/content?path=[&overwrite=1]` | Body is the file. `201 Entry`; `409 exists` without overwrite |
| POST | `/api/v1/fs/mkdir` | `{path}` → `201 Entry` |
| POST | `/api/v1/fs/rename` | `{from, to, overwrite?}` → `Entry`. Same filesystem only (`409 other_drive` otherwise); without `overwrite` an existing `to` gives `409 exists`, atomically |
| POST | `/api/v1/fs/trash` | `{paths: []}` → `{results: [{path, name?, error?, code?}]}` |

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
- `POST /api/auth/unlock` for the lock screen.
