# Tilde's Xpra client (D78 stage 2, D80)

Stage 1 (D78) shows the user's Xpra session through Xpra's own HTML5
client, in one Tilde window (Host Apps) from the Xpra port; the host's
windows float inside it. Stage 2 replaces that with Tilde's own client of
Xpra's protocol, so each host window is a Tilde window: its own title bar
and frame, snapping, the window list, Alt+Tab, minimize and Always on Top.
It reuses stage 1's session, launch and socket (`worker/xpra.go`,
`worker/hostapps.go`).

This document is the plan: how the client is built, what it must get
right, and the steps it lands in. Each step is a roadmap item
(`docs/roadmap.md`, Phase 7). Research for it was done on 2026-10-30
against Xpra 6.0 to 8.0 (master) and xpra-html5 v21 and v22; the sources
are at the end.

## Shape

```
browser (Tilde's origin)                    worker (the user)          Xpra
┌──────────────────────────────────┐
│ HostWindow app, one per X window │
│   <canvas>, input → packets      │
│ hostWindows store (Pinia)        │   WebSocket      ┌────────────┐  unix  ┌──────┐
│   XpraClient ── framing, rencode ├─/api/v1/host-──▶│ byte relay ├───────▶│ xpra │
│   window map, focus, geometry    │  apps/connect    └────────────┘ socket └──────┘
│ decode worker (images, WebCodecs)│
│ popup layer (menus, tooltips)    │
└──────────────────────────────────┘
```

- **Transport.** A new worker endpoint, `GET /api/v1/host-apps/connect`,
  takes a WebSocket on Tilde's own origin (session cookie, the Origin
  check, `423` while locked; locking closes it, as it does terminals) and
  relays its binary messages, as a byte stream, to and from the session's
  socket with the existing `xpraSession.dial`. Xpra tells its own
  protocol from HTTP by the first byte (`P`), so the worker speaks
  neither: the browser frames packets itself, and message boundaries do
  not matter. Like the other endpoints, it never starts the session; the
  desktop does (`/host-apps/session`, `/host-apps/launch`).
- **Protocol** (`web/src/xpra/`): the 8-byte packet header (`P`, flags,
  compression, chunk index, big-endian length), raw chunks put back into
  their packet, LZ4 blocks, and rencodeplus, the only packet encoding
  Xpra 6 has. Byte strings and text are told apart on the wire (`len/`
  and `len:`), and stay `Uint8Array` and `string` in TypeScript. Brotli
  is not asked for.
- **Packet names** go through one table. Xpra 7 renamed most packets
  (`new-window` is `window-create`, `draw` is `window-draw`) and still
  takes the old names by default; xpra-html5 v22 speaks only the new ones
  and needs Xpra 7. Tilde speaks Xpra 6's names first, with the table
  ready for 7.
- **The client** (`XpraClient`): hello and the server's hello, pings
  both ways (every 5 s; 15 s unanswered is a lost connection), and
  dispatch of packets by name to the store. No DOM in it, so it is tested
  with Vitest against recorded packets.
- **The store** (`stores/hostWindows.ts`): owns the one connection per
  browser, maps Xpra's window ids to Tilde window ids, opens and closes
  Tilde windows through the window store (as the start menu does: it is
  shell, not an app), and keeps what each window needs (metadata, size
  constraints, its canvas).
- **The app** (`apps/host-window/`, hidden from the menu): one per host
  window, a `<canvas>` the size of the window's content. It reports its
  content area's geometry and turns pointer and key events into packets.
  It reaches the store through the shell API (`ctx`), never the window
  store.
- **Decoding** in a Worker: `createImageBitmap` for PNG, JPEG and WebP,
  `ImageData` for raw RGB, later WebCodecs' `VideoDecoder` for H.264,
  VP8 and VP9. The bitmap is drawn on the window's canvas on the next
  animation frame, then acknowledged.
- **Popups** (menus, tooltips, dropdowns: X's override-redirect windows)
  are not Tilde windows: they are canvases in a layer above the windows,
  placed relative to the window they belong to.

## What has to be right

### One root, Tilde's desktop

Xpra runs a virtual X screen (Xvfb). In seamless mode the position a
client reports for a window *becomes* the X window's position on that
screen, and apps place their menus and dialogs in screen coordinates. So:

- The X screen is Tilde's desktop: the hello's `desktop_size` and one
  monitor (`monitors`, its `workarea` without the panel) are the browser's
  desktop, and `configure-display` follows the browser window's resizes.
- Each host window's position is its **content area's** top-left in
  desktop coordinates, below Tilde's title bar and inside its border, not
  the frame's. Every move, resize, maximize, tile and restore sends it
  (`configure-window`), with the size; `map-window` sends it first.
- A window the app moves or resizes itself (`window-move-resize`,
  `window-resized`) moves the Tilde window, title bar and all, around
  that content area. Resize races are matched by `resize_counter`.
- Popups arrive in screen coordinates. They are placed at their offset
  from their parent's reported position, added to where the parent is
  drawn, so a popup lands right even if the two disagree for a moment.
  The parent is `transient-for`, or as Xpra's GTK client guesses it:
  the window of the same process, or the focused one.
- Pointer events carry both screen and window-relative coordinates; Xpra
  uses its own geometry plus the relative ones when it knows the window,
  so clicks land even mid-move.

HiDPI is not stage 2's first concern: one CSS pixel is one X pixel, as
in xpra-html5. Sharp text on 2× screens (a larger X screen drawn at half
size, or apps scaled with `GDK_SCALE`) is a later step.

### Windows

| Xpra | Tilde |
| --- | --- |
| `new-window` | `wm.open('host-window', {wid})` at the given place and size, then `map-window` |
| `lost-window` | the Tilde window closes (no close guard runs) |
| `window-metadata`: `title`, `window-icon` (PNG) | `ctx.setTitle`, the window's icon |
| `size-constraints` (min, max) | the frame's minimum and maximum size |
| `transient-for`, `modal` | `parentId`: stays above its parent, hides and closes with it; modal blocks the parent's input |
| `window-type` DIALOG, UTILITY | a dialog frame, centred on the parent when the app gives no position |
| `maximized`, `fullscreen`, `iconic`, `above` | the window's state, both ways (`configure-window` state) |
| `decorations: false` (GTK header bars) | a frameless Tilde window; the app's own buttons and drags (`initiate-moveresize`) drive Tilde's move and resize |
| close button | `close-window`; the frame stays until `lost-window` (the app may ask to save first) |
| minimize | `unmap-window` iconified; restore is `map-window` and `focus` |
| focus | `focus` with the window id, `focus 0` when a Tilde app has it; keys go to the focused window |
| `raise-window` | focus it |

Host windows are not saved with the layout, as dialogs are not: after a
reload the client connects again, Xpra sends every window again, and
each becomes a Tilde window once more. Where they were is kept in the
session (Xpra's own geometry), so they come back where they were.

### Input

- Pointer: `pointer-position`, `button-action` (browser buttons 0, 1, 2
  are X's 1, 2, 3), the wheel as `wheel-motion` when the server says
  `wheel.precise`, else button 4 to 7 clicks per notch. The pointer is
  captured during a drag inside a window, so a drag past its edge goes on.
- Keys: `key-action` with the X keysym name from `KeyboardEvent.code`
  first, then `key`, the numpad and dead keys (tables as xpra-html5's
  `Keycodes.js`, which has `ControlRight` wrong). Xpra turns names into
  keycodes with its own keymap; the browser's layout is sent once
  (`keyboard-config`, or `keymap-changed` on older servers). Tilde's own
  shortcuts (Super, Ctrl+Alt+L, Alt+Tab, Alt+F1) stay Tilde's.
- Cursor: Xpra's `cursor` packets (PNG with a hot spot) become the
  canvas's CSS cursor.

### Several browsers

Xpra drives one client at a time; a second takes the session over
(`steal`), and two that both reconnect on loss take it back and forth for
ever. One user may be signed in from several browsers (D47). So:

- A browser connects when its desktop wants host apps (a launch, or host
  windows it had before a reload), with a `uuid` of its own.
- A browser whose session was taken does not reconnect by itself. Its
  host windows close, and a notice says the host apps are open in
  another browser, with Take Over.
- Reloads and unlocking reconnect, as they are this browser's own.
- Stage 1's Host Apps window is a client too: until it goes (the last
  step), opening it takes the session from Tilde's client, and the other
  way round.

### Security

Rules 5, 6 and 11 hold; the plan changes who talks to Xpra, so it is
spelt out here and in `docs/security.md` as the steps land.

- **Tilde's origin talks to Xpra.** Stage 1 kept Xpra on a port of its
  own so that local web apps' pages could not drive it (D78). Tilde's
  own origin can already run commands (terminals), so the browser
  reaching Xpra from there gives it nothing new; local web apps stay on
  the app port, with no way to `/api/v1/`. Xpra's commands, files, URLs,
  printing and scripts stay off in Xpra itself.
- **What Xpra sends is untrusted.** Host apps choose window titles,
  class names, icons, cursors, clipboard contents and pixels, and they
  now reach Tilde's origin rather than a frame of another origin. Text
  is only ever shown as text (Vue interpolation, never `v-html`);
  icons, cursors and frames are decoded as images (`createImageBitmap`),
  never put into markup; a cursor becomes a `data:image/png;base64` URL
  built by Tilde from bytes, and only after it decodes. Sizes are bound
  (windows, icon and cursor sizes, draw rectangles inside their window,
  chunks, packet length), so a host app cannot use up the tab's memory.
- **The CSP stays as it is.** Decoding uses the browser's own decoders
  (images, WebCodecs) in a Worker loaded from `'self'`: no WebAssembly,
  no `wasm-unsafe-eval`, no `blob:` scripts.
- **The worker only relays bytes** between the WebSocket and the socket
  it already dials; it parses nothing and listens on nothing.
- When stage 1 goes, so do `--html=on`, the Xpra port, its handoff and
  cookie, and the need for xpra-html5: less to defend.

### Licences

xpra-html5 is MPL-2.0 without the "incompatible with secondary licenses"
notice, so its files may be combined with Tilde (AGPL-3.0-or-later), but
a file copied or ported from it stays MPL-2.0. Tilde's client is written
for Tilde, with xpra-html5 and Xpra's own docs as references. The
keysym tables, which are data, may be ported: such a file keeps its
MPL-2.0 header and is listed in `packaging/deb/copyright`. Xpra itself
is GPL-2.0-or-later, which Tilde may take as GPL-3.0 and so use under
AGPL-3.0-or-later.

## Steps

Each step lands on its own, with tests, and leaves host apps working.
Until step 9, Tilde's client is chosen in System Settings (Host apps:
"In Tilde windows (experimental)"); stage 1's window stays the default.

1. **Connection and protocol.** The worker's `/api/v1/host-apps/connect`
   relay (Go test: a fake socket on the far side); framing, raw chunks,
   LZ4 and rencodeplus in TypeScript, tested with vectors made by Xpra's
   own Python encoder; `XpraClient` with hello, ping and disconnect; the
   packet name table. A Playwright test in the VM connects and reads the
   server's hello (Xpra's version). Nothing shown yet.
2. **Windows appear.** The setting; `new-window` and `lost-window`
   become Tilde windows with title and icon; drawing with `rgb24`,
   `rgb32`, `png`, `jpeg`, `webp`, `scroll` and `void`, decoded in a
   Worker and acknowledged (`damage-sequence`, or Xpra slows down and
   stops sending); close asks the app. Reload and unlock reconnect.
   Test: XTerm opens as a Tilde window, its title follows its shell.
3. **Input.** Pointer, buttons, wheel, keyboard (keysym tables and the
   layout), focus both ways, cursors. Host apps become usable. Test: type
   into XTerm, a file appears.
4. **Window management.** Moves, resizes, maximize, tiling, minimize
   and Always on Top told to Xpra; the app's own moves and resizes told
   to Tilde; size constraints; the X screen following the browser's size;
   dialogs (`transient-for`, `modal`) as Tilde dialogs.
5. **Popups.** Override-redirect windows in a layer above the windows,
   placed by their parent: menus, dropdowns, tooltips. A press outside a
   menu closes it, as on X (a press sent to the screen outside it, or a
   focus change; to be found out in the VM).
6. **Clipboard and sound.** Copy and paste both ways through the
   browser's clipboard (written when Xpra offers it, read on paste
   keys and clicks), as xpra-html5 does; host apps' sound through
   `MediaSource` (Opus in WebM), as stage 1 has it now (D79); the bell.
7. **Header-bar apps.** Frameless windows for `decorations: false`;
   `initiate-moveresize` starts Tilde's move or resize from the app's
   own title bar; shadows outside the content (`has-alpha`) left out of
   the frame.
8. **Speed.** Video encodings through WebCodecs (H.264, then VP8 and
   VP9) for windows that change a lot; drawing in the Worker on an
   `OffscreenCanvas`; fewer refreshes for minimized windows and hidden
   tabs; then HiDPI.
9. **Tilde's client by default, stage 1 gone.** Several browsers (take
   over, no reconnect loops); the setting removed; the Host Apps window,
   the Xpra port (`--xpra-listen`, `--xpra-origin`, its handoff and
   cookie), `--html=on` and the need for xpra-html5 removed; docs
   (architecture, security, api, development) brought up to date.

Steps 1 to 4 make host apps usable as Tilde windows; 5 to 7 make them
pleasant; 8 makes them fast; 9 tidies up.

## Open questions

- **Which Xpra.** The VM installs xpra.org's current stable packages;
  step 1 records which version that is (and the user's laptop's), and
  whether Tilde should require 6, 7 or both. The packet table covers
  both either way.
- **Closing a menu from outside.** Xpra 6 has no packet for it (Xpra 8
  adds `window-dismiss`); step 5 finds out what works.
- **HiDPI**: scaling the X screen or the apps (step 8).
- **Sharing instead of taking over.** Xpra can share a session between
  clients (`--sharing=yes`), each drawing every window; whether one
  user's two browsers should show host windows at once is left for after
  step 9.

## Sources

- Xpra: [github.com/Xpra-org/xpra](https://github.com/Xpra-org/xpra),
  `docs/Network/Protocol.md` (master: the new protocol in full),
  `xpra/net/protocol/header.py`, `xpra/net/packet_type.py` (7.x: old
  and new names), `xpra/server/window/metadata.py`,
  `xpra/x11/server/seamless.py`, `xpra/server/auth/peercred.py`,
  `xpra/client/mixins/windows.py` (popup parents, transients).
- xpra-html5, the `v21.x` branch (Xpra 6):
  [github.com/Xpra-org/xpra-html5](https://github.com/Xpra-org/xpra-html5),
  `html5/js/Protocol.js`, `Client.js` (hello, input, clipboard),
  `Window.js` (painting), `DecodeWorker.js`, `VideoDecoder.js`,
  `Keycodes.js`, `MediaSourceUtil.js`, `lib/rencode.js`.
- [andersevenrud/xpra-html5-client](https://github.com/andersevenrud/xpra-html5-client):
  an unfinished TypeScript client (MPL-2.0, old protocol), useful for
  its types and its window-manager split.
