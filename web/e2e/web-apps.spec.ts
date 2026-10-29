import { expect, test, windowNamed } from './fixtures'

// Local web apps (D76): a web server of the test user's opens in a window
// from Tilde's app port, another origin, and its pages cannot use Tilde's
// API with the user's cookie.
test('a local web app opens in a window, from another origin', async ({ desktop: page, api, home, scratch }) => {
  const port = 8765
  const site = `${scratch}/site`
  const probe = `${home}/made-by-the-app`
  await api.mkdir(site)
  await api.write(
    `${site}/index.html`,
    '<html><head><title>Tilde Test App</title></head><body>hello from the app</body>',
  )
  await api.remove(probe)

  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  await expect(term.locator('.xterm-rows')).toContainText('$', { timeout: 10_000 })
  await term.locator('.xterm').click()
  await page.keyboard.type(`cd '${site}' && python3 -m http.server ${port} --bind 127.0.0.1\n`)
  await expect(term.locator('.xterm-rows')).toContainText(`port ${port}`)

  await page.keyboard.press('Alt+F1')
  await page.getByRole('searchbox', { name: 'Search applications' }).fill('Web Apps')
  await page.keyboard.press('Enter')
  const list = windowNamed(page, 'Web Apps').getByRole('list', { name: 'Web apps' })
  const item = list.getByRole('listitem').filter({ hasText: 'Tilde Test App' })
  await expect(item).toContainText(`Port ${port} · python3 -m http.server`)
  await item.getByRole('button', { name: 'Open' }).click()

  const win = windowNamed(page, `Tilde Test App (port ${port})`)
  await expect(win.frameLocator('iframe').getByText('hello from the app')).toBeVisible()
  const main = new URL(page.url())
  const frame = page.frames().find((f) => f.url().includes(`/proxy/${port}/`))
  expect(frame).toBeTruthy()
  const appOrigin = new URL(frame!.url())
  expect(appOrigin.hostname).toBe(main.hostname)
  expect(Number(appOrigin.port)).toBe(Number(main.port) + 1)

  // The app's page tries Tilde's API with the user's cookie: a GET it may
  // not read, and a POST that would make a folder. Neither gets through.
  const read = await frame!.evaluate(async (origin) => {
    try {
      const r = await fetch(`${origin}/api/v1/hello`, { credentials: 'include' })
      return `read ${r.status}`
    } catch {
      return 'refused'
    }
  }, main.origin)
  expect(read).toBe('refused')
  await frame!.evaluate(
    async ([origin, path]) => {
      await fetch(`${origin}/api/v1/fs/mkdir`, {
        method: 'POST',
        mode: 'no-cors',
        credentials: 'include',
        headers: { 'Content-Type': 'text/plain' },
        body: JSON.stringify({ path }),
      }).catch(() => {})
    },
    [main.origin, probe],
  )
  expect(await api.exists(probe)).toBe(false)

  await win.getByTitle('Close').click()
  await term.getByTitle('Close').click()
  await expect(term).toHaveCount(0)
})
