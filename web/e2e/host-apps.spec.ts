import { expect, test, user, windowNamed } from './fixtures'

// Host apps through Xpra (D78): the menu lists the host's .desktop apps;
// one starts in the user's Xpra session, shown by Xpra's HTML5 client in
// a window from the Xpra port, an origin of its own.
test('a host app from the menu opens in the Xpra window', async ({ desktop: page }) => {
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
})
