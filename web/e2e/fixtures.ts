// Shared fixtures: an API client that acts as the test user, a logged-in
// desktop, and a scratch folder that is removed afterwards.
import { createHmac } from 'node:crypto'
import { crc32, deflateSync } from 'node:zlib'
import { test as base, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'

export const user = process.env.TILDE_USER!
export const password = process.env.TILDE_PASS!
/** An account that pam_oath asks a TOTP code of (D43), and its hex secret. */
export const twoFactorUser = process.env.TILDE_2FA_USER ?? ''
const twoFactorSecret = process.env.TILDE_2FA_SECRET ?? ''

/**
 * Codes for twoFactorUser, as pam_oath's "HOTP/T30" makes them (RFC 6238,
 * SHA-1, 6 digits). Each call gives the next one: pam_oath refuses a code
 * twice, and the VM script resets its record before each run.
 */
export function totpCodes(): () => string {
  let counter = Math.floor(Date.now() / 30_000)
  return () => {
    const msg = Buffer.alloc(8)
    msg.writeBigUInt64BE(BigInt(counter++))
    const sum = createHmac('sha1', Buffer.from(twoFactorSecret, 'hex')).update(msg).digest()
    const off = sum[sum.length - 1]! & 0x0f
    return String((sum.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, '0')
  }
}

/** The worker API as the test user, sharing the page's session cookie. */
export class Api {
  constructor(
    private readonly request: APIRequestContext,
    private readonly origin: string,
  ) {}

  // State-changing requests must carry a same-origin Origin header.
  private get headers() {
    return { Origin: this.origin }
  }

  async login(username = user, pass = password) {
    const res = await this.request.post('/api/auth/login', {
      data: { username, password: pass },
      headers: this.headers,
    })
    expect(res.status(), await res.text()).toBe(200)
    return (await res.json()) as { user: string; home: string }
  }

  async mkdir(path: string) {
    const res = await this.request.post('/api/v1/fs/mkdir', { data: { path }, headers: this.headers })
    expect([201, 409]).toContain(res.status())
  }

  async write(path: string, body: string | Buffer) {
    const res = await this.request.put(`/api/v1/fs/content?path=${encodeURIComponent(path)}&overwrite=1`, {
      data: body,
      headers: this.headers,
    })
    expect(res.status(), await res.text()).toBe(201)
  }

  async exists(path: string) {
    const res = await this.request.get(`/api/v1/fs/stat?path=${encodeURIComponent(path)}`)
    return res.status() === 200
  }

  /** Deletes paths permanently through a job and waits until they are gone. */
  async remove(...paths: string[]) {
    const res = await this.request.post('/api/v1/jobs', {
      data: { op: 'delete', sources: paths },
      headers: this.headers,
    })
    if (res.status() !== 202) return
    // Not the job's state: an open desktop dismisses finished jobs.
    for (const p of paths) await expect.poll(() => this.exists(p)).toBe(false)
  }

  /** Drops a background picked in Tilde, so the desktop follows Cinnamon (or the theme). */
  async resetBackground() {
    const res = await this.request.put('/api/v1/desktop/background', { data: { reset: true }, headers: this.headers })
    expect(res.status(), await res.text()).toBe(200)
  }

  /** Removes every notification from the list (D70). */
  async clearNotifications() {
    const res = await this.request.post('/api/v1/notifications/clear', { data: {}, headers: this.headers })
    expect(res.status(), await res.text()).toBe(204)
  }

  /** Puts System Settings back to the defaults (D72). */
  async resetSettings() {
    const res = await this.request.put('/api/v1/settings', {
      data: {
        lock: { own: false, enabled: true, minutes: 15 },
        appearance: { mode: 'desktop', accent: 'desktop' },
        effects: { animations: true },
        panel: { resources: true, network: true, notifications: true, fullscreen: true },
      },
      headers: this.headers,
    })
    expect(res.status(), await res.text()).toBe(200)
  }

  /** Clears Tilde's saved desktop layout (an empty saved layout: no Nemo seed). */
  async resetDesktopLayout() {
    const res = await this.request.post('/api/v1/desktop/layout', {
      data: { positions: {}, replace: true },
      headers: this.headers,
    })
    expect(res.status(), await res.text()).toBe(200)
  }

  /** Unmounts the volume labelled name, if it is mounted. */
  async unmount(name: string) {
    const res = await this.request.get('/api/v1/drives')
    const { volumes } = (await res.json()) as { volumes: { id: string; name: string; mountPoint?: string }[] }
    const vol = volumes.find((v) => v.name === name)
    expect(vol, `${name} is not listed`).toBeTruthy()
    if (!vol!.mountPoint) return
    const un = await this.request.post('/api/v1/drives/unmount', { data: { id: vol!.id }, headers: this.headers })
    expect(un.status(), await un.text()).toBe(204)
  }

  async places() {
    const res = await this.request.get('/api/v1/places')
    return ((await res.json()) as { places: { id: string; path: string }[] }).places
  }
}

interface Fixtures {
  api: Api
  /** The test user's home folder; the API is logged in. */
  home: string
  /** A fresh folder in the home folder, deleted after the test. */
  scratch: string
  /** The page with the desktop showing. */
  desktop: Page
}

let seq = 0

export const test = base.extend<Fixtures>({
  api: async ({ page, baseURL }, use) => {
    await use(new Api(page.request, new URL(baseURL!).origin))
  },
  home: async ({ api }, use) => {
    const { home } = await api.login()
    await use(home)
  },
  scratch: async ({ api, home }, use, info) => {
    const dir = `${home}/pw-${Date.now().toString(36)}-${info.workerIndex}-${seq++}`
    await api.mkdir(dir)
    await use(dir)
    await api.remove(dir)
  },
  desktop: async ({ page, home }, use) => {
    void home // logged in through the API
    await page.goto('/')
    await expect(page.getByRole('navigation', { name: 'Panel' })).toBeVisible()
    await use(page)
  },
})

export { expect }

/** A PNG of one color, w × h pixels, for image tests. */
export function png(w: number, h: number, rgb: [number, number, number] = [200, 60, 60]): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(body))
    return Buffer.concat([len, body, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0)
  ihdr.writeUInt32BE(h, 4)
  ihdr.set([8, 2, 0, 0, 0], 8) // 8-bit RGB
  const row = Buffer.alloc(1 + 3 * w)
  for (let x = 0; x < w; x++) row.set(rgb, 1 + 3 * x)
  const pixels = Buffer.concat(Array.from({ length: h }, () => row))
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(pixels)),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

/** A WAV file: a tone of seconds length, 8 kHz mono, for media tests. */
export function wav(seconds: number, hz = 440): Buffer {
  const rate = 8000
  const samples = Math.round(seconds * rate)
  const buf = Buffer.alloc(44 + samples * 2)
  buf.write('RIFF', 0, 'ascii')
  buf.writeUInt32LE(36 + samples * 2, 4)
  buf.write('WAVEfmt ', 8, 'ascii')
  buf.writeUInt32LE(16, 16) // fmt chunk size
  buf.writeUInt16LE(1, 20) // PCM
  buf.writeUInt16LE(1, 22) // mono
  buf.writeUInt32LE(rate, 24)
  buf.writeUInt32LE(rate * 2, 28)
  buf.writeUInt16LE(2, 32)
  buf.writeUInt16LE(16, 34)
  buf.write('data', 36, 'ascii')
  buf.writeUInt32LE(samples * 2, 40)
  for (let i = 0; i < samples; i++)
    buf.writeInt16LE(Math.round(8000 * Math.sin((2 * Math.PI * hz * i) / rate)), 44 + i * 2)
  return buf
}

/** A window frame by its title. */
export function windowNamed(page: Page, title: string | RegExp): Locator {
  return page.getByRole('dialog', { name: title })
}

/** A desktop icon by its label. */
export function desktopIcon(page: Page, name: string): Locator {
  return page.locator('.desktop-icons .desktop-icon', {
    has: page.locator('.label', { hasText: new RegExp(`^${escape(name)}$`) }),
  })
}

/** An item in a Files window by name. */
export function fileItem(win: Locator, name: string): Locator {
  return win.locator('[data-item]', { has: win.page().locator('.label', { hasText: new RegExp(`^${escape(name)}$`) }) })
}

function escape(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
