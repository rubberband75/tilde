import { expect, test, windowNamed } from './fixtures'

// Programs' notifications (D70): in the VM nothing else has the name on
// the test user's session bus, so Tilde shows them.
test('a program’s notification: a toast with buttons, and the panel’s list', async ({ desktop: page, api }) => {
  await api.clearNotifications()
  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  const screen = term.locator('.xterm-rows')
  await expect(screen).toContainText('$', { timeout: 10_000 })
  await term.locator('.xterm').click()

  // notify-send waits for a button and prints its key.
  await page.keyboard.type("echo picked=$(notify-send -a Backups -A undo=Undo -A keep=Keep 'Backup done' '12 files')\n")
  const toast = page.getByRole('status', { name: 'Backups: Backup done' })
  await expect(toast).toBeVisible()
  await expect(toast).toContainText('12 files')
  await toast.getByRole('button', { name: 'Keep' }).click()
  await expect(screen).toContainText('picked=keep')
  await expect(toast).toHaveCount(0)

  // Expired toasts stay in the panel's list; markup is shown as text.
  await term.locator('.xterm').click()
  await page.keyboard.type('notify-send -t 1 "Second" "<b>bold</b> &amp; plain"\n')
  const applet = page.getByRole('button', { name: /^Notifications: \d+ notification/ })
  await expect(applet).toHaveAttribute('aria-label', 'Notifications: 2 notifications')
  await applet.click()
  const list = page.getByRole('list', { name: 'Notifications' })
  await expect(list.getByText('bold & plain')).toBeVisible()
  await expect(list.getByText('Backup done')).toBeVisible()
  await list.getByRole('button', { name: 'Remove Second' }).click()
  await expect(list.getByText('bold & plain')).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear', exact: true }).click()
  await expect(page.getByText('No notifications')).toBeVisible()
  await page.keyboard.press('Escape')

  await term.getByTitle('Close').click()
  await expect(term).toHaveCount(0)
})
