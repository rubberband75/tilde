import { defineStore } from 'pinia'
import { hostAppsApi } from '@/api/hostApps'
import { useNotices } from '@/stores/notices'
import { useWindows } from '@/stores/windows'
import { helloCaps, windowCaps, XpraClient } from '@/xpra/client'
import { DrawDecoder } from '@/xpra/decoder'
import {
  drawFits,
  imageSize,
  imageType,
  isDict,
  isInt,
  isRaw,
  MAX_WINDOW_SIDE,
  parseDraw,
  type Draw,
} from '@/xpra/draw'
import type { Packet } from '@/xpra/framing'
import type { Dict } from '@/xpra/rencode'

/** More windows than anyone keeps open: a host app cannot use up the tab. */
const MAX_WINDOWS = 64
const MAX_TITLE = 512
/** A window's icon: Tilde asks for 64×64, and takes a little over. */
const MAX_ICON_SIDE = 256
const MAX_ICON_BYTES = 1 << 20
/** Canvases, in pixels: four bytes each, whatever the window shows. */
const MAX_WINDOW_PIXELS = 64_000_000
const MAX_TOTAL_PIXELS = 256_000_000
/**
 * Draws waiting to be painted. Xpra's own flow control keeps far below this;
 * past it, whatever is sending does not care to.
 */
const MAX_PENDING_DRAWS = 256
const MAX_PENDING_BYTES = 256 << 20
const MAX_MESSAGE = 200
/** Control and bidi characters, which could make a notice read as something else. */
// eslint-disable-next-line no-control-regex
const UNPRINTABLE = /[\u0000-\u001f\u007f-\u009f\u200e\u200f\u202a-\u202e\u2066-\u2069]/g

/** Xpra's words, safe for a notice or to send back. */
function shortMessage(s: string): string {
  return s.replace(UNPRINTABLE, ' ').slice(0, MAX_MESSAGE)
}

interface HostWindow {
  wid: number
  windowId: string
  /** Lives here, not in the app: drawing starts before the app has loaded. */
  canvas: HTMLCanvasElement
  context: CanvasRenderingContext2D | null
  /** Paints come in the server's order: a scroll moves what came before it. */
  queue: Promise<void>
  /** The icon shown, to give back when it changes or the window goes. */
  iconUrl?: string
  /** The latest icon asked for: an older one finishing late is dropped. */
  iconSeq: number
}

/** The frame around a window's content: its border, and the title bar. */
function frameInsets() {
  const css = getComputedStyle(document.documentElement).getPropertyValue('--wm-titlebar-height')
  return { border: 1, titlebar: parseInt(css) || 32 }
}

/**
 * The host's windows as Tilde windows (D80, docs/xpra-client.md): the one
 * connection of this browser to the user's Xpra session, and a Tilde window
 * for each X window it reports. Windows here are the ones Xpra has: not saved
 * with the layout, and sent again on connecting after a reload.
 *
 * What Xpra sends is untrusted. Titles only ever reach text; pictures are
 * checked by decoding them; sizes and counts are bounded.
 */
export const useHostWindows = defineStore('hostWindows', () => {
  const wm = useWindows()
  const notices = useNotices()
  const windows = new Map<number, HostWindow>()
  /** Stays for a page's life, so a reconnect replaces its old connection. */
  let uuid = ''
  /** The canvases' pixels, against MAX_TOTAL_PIXELS. */
  let pixels = 0
  /** Draws of this connection not painted yet. */
  let pending = { draws: 0, bytes: 0 }
  let client: XpraClient | null = null
  let connecting: Promise<void> | null = null
  /** Goes up on disconnecting: a connection still being made is then dropped. */
  let generation = 0
  const decoder = new DrawDecoder()

  /**
   * Connects to the running session, if not connected. It does not start
   * the session, and rejects if none runs.
   */
  function connect(): Promise<void> {
    if (client && !client.closed) return Promise.resolve()
    if (connecting) return connecting
    const attempt: Promise<void> = open().finally(() => {
      if (connecting === attempt) connecting = null
    })
    connecting = attempt
    return attempt
  }

  async function open() {
    const mine = generation
    const transport = await hostAppsApi.connect()
    if (mine !== generation) {
      transport.close()
      return
    }
    // The Host Apps window is a client too, and would take the session back.
    for (const w of wm.windows.filter((w) => w.appId === 'xpra')) wm.close(w.id)
    uuid ||= crypto.randomUUID()
    pending = { draws: 0, bytes: 0 }
    const c = new XpraClient(transport, {
      caps: {
        ...helloCaps({ uuid, ui: true }),
        ...windowCaps({ w: wm.area.w, h: Math.max(window.innerHeight, wm.area.h), workHeight: wm.area.h }),
      },
    })
    c.onPacket = handle
    c.onClose = (reason) => {
      if (client !== c) return
      client = null
      closeAll()
      decoder.close()
      notices.push(
        reason.startsWith('new client')
          ? 'Host apps are now open in another browser or window.'
          : `Host apps disconnected: ${shortMessage(reason)}`,
        'error',
      )
    }
    client = c
    c.start()
  }

  /** Ends the connection, this browser's choice; the apps keep running. */
  function disconnect() {
    generation++
    connecting = null
    const c = client
    client = null
    c?.close('the desktop closed')
    closeAll()
    decoder.close()
  }

  function closeAll() {
    const all = [...windows.values()]
    windows.clear()
    for (const w of all) {
      release(w)
      wm.close(w.windowId)
    }
  }

  /** Gives back what a window held, at once: apps open and close many. */
  function release(w: HostWindow) {
    pixels -= w.canvas.width * w.canvas.height
    w.canvas.width = 0
    if (w.iconUrl) URL.revokeObjectURL(w.iconUrl)
  }

  function handle(p: Packet) {
    switch (p[0]) {
      case 'new-window':
        newWindow(p)
        break
      case 'lost-window':
        lostWindow(p)
        break
      case 'window-metadata':
        if (isInt(p[1]) && isDict(p[2])) applyMetadata(p[1], p[2])
        break
      case 'window-icon':
        void windowIcon(p)
        break
      case 'draw':
        draw(p)
        break
    }
  }

  function newWindow(p: Packet) {
    const [, wid, x, y, w, h, metadata] = p
    if (!isInt(wid) || !isInt(x) || !isInt(y) || !isInt(w) || !isInt(h)) return
    if (windows.has(wid) || windows.size >= MAX_WINDOWS) return
    if (w < 1 || h < 1 || w > MAX_WINDOW_SIDE || h > MAX_WINDOW_SIDE) return
    if (w * h > MAX_WINDOW_PIXELS || pixels + w * h > MAX_TOTAL_PIXELS) return
    const windowId = wm.open('host-window', { wid })
    const win = windowId ? wm.get(windowId) : undefined
    if (!windowId || !win) return

    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    pixels += w * h
    windows.set(wid, {
      wid,
      windowId,
      canvas,
      context: canvas.getContext('2d', { alpha: false }),
      queue: Promise.resolve(),
      iconSeq: 0,
    })
    const meta: Dict = isDict(metadata) ? metadata : (Object.create(null) as Dict)
    applyMetadata(wid, meta)

    // Where the app asked, or where the window manager put it; and the
    // frame around the content the app knows of (docs/xpra-client.md).
    const { border, titlebar } = frameInsets()
    const placed = x !== 0 || y !== 0 || meta['set-initial-position'] === true
    wm.setRect(windowId, {
      x: placed ? x - border : win.rect.x,
      y: placed ? y - titlebar - border : win.rect.y,
      w: w + 2 * border,
      h: h + titlebar + 2 * border,
    })
    const at = wm.get(windowId)!.rect
    client?.send('map-window', wid, at.x + border, at.y + titlebar + border, w, h, {})
  }

  function lostWindow(p: Packet) {
    const win = isInt(p[1]) ? windows.get(p[1]) : undefined
    if (!win) return
    windows.delete(win.wid)
    release(win)
    wm.close(win.windowId)
  }

  function applyMetadata(wid: number, meta: Dict) {
    const win = windows.get(wid)
    if (win && typeof meta.title === 'string' && meta.title) wm.setTitle(win.windowId, meta.title.slice(0, MAX_TITLE))
  }

  /** ["window-icon", wid, w, h, "png", data]: shown only if it decodes. */
  async function windowIcon(p: Packet) {
    const [, wid, w, h, encoding, data] = p
    if (!isInt(wid) || !isInt(w) || !isInt(h) || encoding !== 'png' || !(data instanceof Uint8Array)) return
    if (w > MAX_ICON_SIDE || h > MAX_ICON_SIDE || data.length > MAX_ICON_BYTES) return
    const asked = windows.get(wid)
    if (!asked) return
    // What it says it is, not what the packet says: before decoding it.
    const size = imageSize(data, 'image/png')
    if (!size || size.w > MAX_ICON_SIDE || size.h > MAX_ICON_SIDE) return
    const seq = ++asked.iconSeq
    const blob = new Blob([data as Uint8Array<ArrayBuffer>], { type: 'image/png' })
    try {
      ;(await createImageBitmap(blob)).close()
    } catch {
      return // not a picture
    }
    const win = windows.get(wid)
    if (win !== asked || seq !== win.iconSeq) return
    const old = win.iconUrl
    win.iconUrl = URL.createObjectURL(blob)
    wm.setIconSrc(win.windowId, win.iconUrl)
    if (old) URL.revokeObjectURL(old)
  }

  function ack(d: Draw, decodeTime: number, message = '') {
    // Xpra sends it again some other way, so a mistake here would not show.
    if (decodeTime < 0) console.warn(`host window: draw refused: ${shortMessage(message)}`)
    client?.send('damage-sequence', d.seq, d.wid, d.w, d.h, decodeTime, shortMessage(message))
  }

  function draw(p: Packet) {
    const d = parseDraw(p)
    if (!d) throw new Error('a draw that cannot be read')
    const win = windows.get(d.wid)
    // A window Tilde does not show (a menu, until those come): Xpra still
    // waits to hear, or it slows down and stops.
    if (!win) {
      ack(d, 0)
      return
    }
    if (!drawFits(d, win.canvas.width, win.canvas.height)) {
      ack(d, -1, 'outside the window')
      return
    }
    const type = imageType(d.coding)
    if (type) {
      const size = imageSize(d.data, type)
      if (!size || size.w > d.w || size.h > d.h) {
        ack(d, -1, 'an image of another size')
        return
      }
    }
    const load = pending
    if (load.draws >= MAX_PENDING_DRAWS || load.bytes + d.data.length > MAX_PENDING_BYTES) {
      client?.close('too many draws waiting')
      return
    }
    load.draws++
    load.bytes += d.data.length
    const start = performance.now()
    let decoded: Promise<ImageBitmap | null> = Promise.resolve(null)
    if (isRaw(d.coding) || type) {
      decoded = decoder.decode(d)
      // Looked at in its turn, below.
      decoded.catch(() => {})
    }
    win.queue = win.queue.then(async () => {
      try {
        paint(win, d, await decoded)
        ack(d, Math.round((performance.now() - start) * 1000))
      } catch (e) {
        // A negative time: Xpra sends that part again some other way.
        ack(d, -1, (e as Error).message)
      } finally {
        load.draws--
        load.bytes -= d.data.length
      }
    })
  }

  function paint(win: HostWindow, d: Draw, bitmap: ImageBitmap | null) {
    const c = win.context
    if (bitmap) {
      try {
        c?.drawImage(bitmap, d.x, d.y, d.w, d.h)
      } finally {
        bitmap.close()
      }
      return
    }
    if (d.coding === 'scroll') {
      for (const [sx, sy, sw, sh, dx, dy] of d.scrolls)
        c?.drawImage(win.canvas, sx, sy, sw, sh, sx + dx, sy + dy, sw, sh)
    } else if (d.coding !== 'void') {
      throw new Error('unsupported encoding')
    }
  }

  /** The canvas a host window's app shows. */
  function canvasOf(wid: number): HTMLCanvasElement | undefined {
    return windows.get(wid)?.canvas
  }

  /**
   * Asks the app to close the window; it goes when Xpra says it has. Returns
   * false while it may yet refuse (unsaved work), true with no one to ask.
   */
  function requestClose(wid: number): boolean {
    if (!client || client.closed || !windows.has(wid)) return true
    client.send('close-window', wid)
    return false
  }

  return { connect, disconnect, canvasOf, requestClose }
})
