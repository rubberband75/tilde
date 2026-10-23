// Shared fixtures: an API client that acts as the test user, a logged-in
// desktop, and a scratch folder that is removed afterwards.
import { createHmac } from 'node:crypto'
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

  /** Clears Tilde's saved desktop layout (an empty saved layout: no Nemo seed). */
  async resetDesktopLayout() {
    const res = await this.request.post('/api/v1/desktop/layout', {
      data: { positions: {}, replace: true },
      headers: this.headers,
    })
    expect(res.status(), await res.text()).toBe(200)
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
