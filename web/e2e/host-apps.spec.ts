import type { Page } from '@playwright/test'
import { helloCaps, XpraClient, type Transport } from '../src/xpra/client'
import type { Dict } from '../src/xpra/rencode'
import { expect, test, user, windowNamed } from './fixtures'

// Host apps through Xpra (D78): the menu lists the host's .desktop apps;
// one starts in the user's Xpra session, shown by Xpra's HTML5 client in
// a window from the Xpra port, an origin of its own.
test('a host app from the menu opens in the Xpra window', async ({ desktop: page, api, home }) => {
  test.setTimeout(90_000)
  await page.keyboard.press('Alt+F1')
  const search = page.getByRole('searchbox', { name: 'Search applications' })
  await search.fill('XTerm')
  await expect(page.locator('.start-menu .apps li', { hasText: /^XTerm$/ })).toBeVisible({ timeout: 10_000 })
  await page.locator('.start-menu .apps li', { hasText: /^XTerm$/ }).click()

  const win = windowNamed(page, 'Host Apps')
  await expect(win).toBeVisible()
  // Xpra's client draws each host window with its own title bar: XTerm's
  // says xterm, then user@host once its shell runs.
  const title = win.frameLocator('iframe').locator('.windowhead', { hasText: new RegExp(`xterm|${user}@`) })
  await expect(title.first()).toBeVisible({ timeout: 60_000 })
  // From the Xpra port, two up: not the app port, whose pages are others'.
  const frame = page.frames().find((f) => f.url().includes('/xpra/'))
  expect(new URL(frame!.url()).port).toBe(String(Number(new URL(page.url()).port) + 2))

  // Host apps play to the session's own sound server, which Xpra sends to
  // the browser (D79): an app the test adds says which server it has.
  const probe = `${home}/sound-probe.txt`
  const entry = `${home}/.local/share/applications/tilde-sound-probe.desktop`
  if (await api.exists(probe)) await api.remove(probe)
  await api.mkdir(`${home}/.local`)
  await api.mkdir(`${home}/.local/share`)
  await api.mkdir(`${home}/.local/share/applications`)
  await api.write(
    entry,
    `[Desktop Entry]\nType=Application\nName=Tilde Sound Probe\nExec=sh -c "pactl info > ${probe}"\nCategories=Utility;\n`,
  )
  try {
    // The menu by its button: the Xpra window has the keyboard now.
    await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Menu').click()
    await page.getByRole('searchbox', { name: 'Search applications' }).fill('Tilde Sound Probe')
    await page.locator('.start-menu .apps li', { hasText: 'Tilde Sound Probe' }).click()
    await expect.poll(() => api.exists(probe), { timeout: 15_000 }).toBe(true)
    const info = await (await page.request.get(`/api/v1/fs/content?path=${encodeURIComponent(probe)}`)).text()
    expect(info).toMatch(/Server String: .*\/xpra\/\d+\/pulse\/native/)
  } finally {
    await api.remove(entry, probe)
  }
})

/**
 * A WebSocket the page opens on Tilde's origin (its cookie, its Origin),
 * driven from the test: Tilde's own Xpra client runs here, in Node, and
 * its bytes go through the page.
 */
async function pageSocket(page: Page, path: string): Promise<Transport> {
  const t: Transport = {
    send: (b) =>
      void page.evaluate(
        (s) => window.tildeTestSocket!.send(Uint8Array.from(atob(s), (c) => c.charCodeAt(0))),
        Buffer.from(b).toString('base64'),
      ),
    close: () => void page.evaluate(() => window.tildeTestSocket!.close()),
    onData: null,
    onClose: null,
  }
  await page.exposeFunction('tildeTestData', (s: string) => t.onData?.(new Uint8Array(Buffer.from(s, 'base64'))))
  await page.exposeFunction('tildeTestClose', (reason: string) => t.onClose?.(reason))
  await page.evaluate(async (path) => {
    const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${path}`)
    socket.binaryType = 'arraybuffer'
    window.tildeTestSocket = socket
    socket.onmessage = (e) => {
      const b = new Uint8Array(e.data as ArrayBuffer)
      let s = ''
      for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
      void window.tildeTestData!(btoa(s))
    }
    socket.onclose = (e) => void window.tildeTestClose!(e.reason || `closed (${e.code})`)
    await new Promise((resolve, reject) => {
      socket.onopen = resolve
      socket.onerror = reject
    })
  }, path)
  return t
}

declare global {
  interface Window {
    tildeTestSocket?: WebSocket
    tildeTestData?: (s: string) => Promise<void>
    tildeTestClose?: (reason: string) => Promise<void>
  }
}

// Tilde's own Xpra client (D80, docs/xpra-client.md): the worker relays a
// WebSocket on Tilde's origin to the session's socket, and the client
// speaks Xpra's protocol over it. One that shows no windows leaves the
// Host Apps window's client connected.
test("Tilde's Xpra client says hello to the session, and pings it", async ({ desktop: page }) => {
  test.setTimeout(90_000)
  const origin = new URL(page.url()).origin
  const res = await page.request.post('/api/v1/host-apps/session', { headers: { Origin: origin } })
  expect(res.status(), await res.text()).toBe(204)

  const client = new XpraClient(await pageSocket(page, '/api/v1/host-apps/connect'), {
    caps: helloCaps({ uuid: 'tilde-e2e', ui: false }),
  })
  const server = new Promise<Dict>((resolve, reject) => {
    client.onHello = resolve
    client.onClose = (reason) => reject(new Error(reason))
  })
  client.start()
  const hello = await server
  // xpra.org's packages, which Tilde asks for: Xpra 6 or later.
  expect(String(hello.version)).toMatch(/^([6-9]|\d\d)\./)
  expect(hello['packet-types']).toEqual(expect.arrayContaining(['ping', 'ping_echo']))
  console.log(`Xpra ${String(hello.version)}`)

  await expect.poll(() => client.latency, { timeout: 10_000 }).not.toBeNull()
  expect(client.state).toBe('connected')
  client.close('done')
})
