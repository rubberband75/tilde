import { expect, password, test, user, windowNamed } from './fixtures'

test('locking from the menu, staying locked across a reload, unlocking', async ({ desktop: page }) => {
  const panel = page.getByRole('navigation', { name: 'Panel' })
  await panel.getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  const screen = term.locator('.xterm-rows')
  await expect(screen).toContainText('$', { timeout: 10_000 })
  await term.locator('.xterm').click()
  await page.keyboard.type('echo "before=$((40+2))"\n')
  await expect(screen).toContainText('before=42')

  await page.getByTitle('Menu').click()
  await page.getByTitle('Lock Screen').click()
  const lock = page.getByRole('main', { name: 'Locked' })
  const unlock = lock.getByRole('button', { name: 'Unlock' })
  await expect(unlock).toBeVisible()
  await expect(lock.getByRole('heading', { name: user })).toBeVisible()
  // The desktop is gone from the page, and the server refuses this
  // browser, not just the page.
  await expect(panel).toHaveCount(0)
  await expect(term).toHaveCount(0)
  expect((await page.request.get('/api/v1/hello')).status()).toBe(423)

  await page.reload()
  await expect(unlock).toBeVisible()

  await lock.getByLabel('Password').fill(`wrong-${password}`)
  await unlock.click()
  // PAM delays failures by a couple of seconds.
  await expect(lock.getByRole('alert')).toBeVisible({ timeout: 10_000 })

  await lock.getByLabel('Password').fill(password)
  await unlock.click()
  await expect(panel).toBeVisible({ timeout: 10_000 })
  // The terminal window comes back with its shell and output.
  await expect(screen).toContainText('before=42', { timeout: 10_000 })
  await term.getByTitle('Close').click()
  await expect(term).toHaveCount(0)
})

test('Ctrl+Alt+L locks; Switch User leaves the session running', async ({ desktop: page, api }) => {
  await page.keyboard.press('Control+Alt+KeyL')
  const lock = page.getByRole('main', { name: 'Locked' })
  await expect(lock).toBeVisible()

  await lock.getByRole('button', { name: 'Switch User' }).click()
  await expect(page.getByRole('button', { name: 'Log In' })).toBeVisible()
  expect((await page.request.get('/api/v1/hello')).status()).toBe(401)

  // The session was not ended: logging in again rejoins it, unlocked.
  await api.login()
  await page.reload()
  await expect(page.getByRole('navigation', { name: 'Panel' })).toBeVisible()
})
