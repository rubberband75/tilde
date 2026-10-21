import { expect, test, user, windowNamed } from './fixtures'

test('terminal runs a login shell as the user in a web session', async ({ desktop: page }) => {
  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const win = windowNamed(page, /Terminal|@/)
  await expect(win).toBeVisible()
  const screen = win.locator('.xterm-rows')
  await expect(screen).toContainText('$', { timeout: 10_000 })

  await win.locator('.xterm').click()
  await page.keyboard.type('echo "user=$(id -un) type=$XDG_SESSION_TYPE"\n')
  await expect(screen).toContainText(`user=${user} type=web`)

  // Closing the window hangs up the shell.
  await win.getByTitle('Close').click()
  await expect(win).toHaveCount(0)
})
