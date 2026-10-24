import { Api, expect, test } from './fixtures'

// Two browsers of the same user: the panel's applet lists both, and one
// signs the other out (D47). Earlier tests leave their browsers signed in
// too, so the other one is found by what it says it is.
test('the panel lists signed-in browsers and signs one out', async ({ desktop: page, browser, baseURL }) => {
  const other = await browser.newContext({
    ignoreHTTPSErrors: true,
    baseURL,
    userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0',
  })
  try {
    await new Api(other.request, new URL(baseURL!).origin).login()
    const otherPage = await other.newPage()
    await otherPage.goto('/')
    await expect(otherPage.getByRole('navigation', { name: 'Panel' })).toBeVisible()

    // Opening the list asks the server at once.
    await page.getByRole('button', { name: /^\d+ browsers signed in$/ }).click()
    const menu = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: 'Signed-in browsers' }) })
    await expect(menu.locator('li.current')).toContainText('This browser')
    // Earlier runs may have left another one, with no tab open.
    const entry = menu.locator('li', { hasText: 'Firefox on Windows' }).filter({ hasText: 'In use' })
    await expect(entry).toHaveCount(1)
    if (process.env.TILDE_SHOT) await page.screenshot({ path: process.env.TILDE_SHOT })

    await entry.getByRole('button', { name: /^Sign out Firefox on Windows at / }).click()
    await expect(entry).toHaveCount(0)
    await expect(otherPage.getByLabel('User name', { exact: true })).toBeVisible()
  } finally {
    await other.close()
  }
})
