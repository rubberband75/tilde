import type { Page } from '@playwright/test'
import { expect, test, windowNamed } from './fixtures'

async function openSettings(page: Page) {
  await page.keyboard.press('Alt+F1')
  await page.getByRole('searchbox', { name: 'Search applications' }).fill('System Settings')
  await page.keyboard.press('Enter')
  return windowNamed(page, 'System Settings')
}

test('System Settings: the panel, the lock delay and recent logins', async ({ desktop: page, api }) => {
  await api.resetSettings()
  // The lock delay the server gives this browser, as it reports input.
  const lockAfter = async () => {
    const res = await page.request.post('/api/auth/activity', { headers: { Origin: new URL(page.url()).origin } })
    return ((await res.json()) as { lockAfter: number }).lockAfter
  }
  const desktopDelay = await lockAfter()
  try {
    const win = await openSettings(page)
    const monitor = page.locator('.panel .applet.resources')
    await expect(monitor).toBeVisible()

    // Hiding an applet applies at once, and stays after a reload.
    await win.getByRole('button', { name: 'Panel' }).click()
    await win.getByRole('switch', { name: 'System monitor' }).click()
    await expect(monitor).toHaveCount(0)
    await page.reload()
    await expect(page.locator('.panel')).toBeVisible()
    await expect(monitor).toHaveCount(0)

    // Tilde's own lock delay reaches the server.
    const again = windowNamed(page, 'System Settings')
    await again.getByRole('button', { name: 'Screen Lock' }).click()
    await again.getByRole('radio', { name: /Tilde’s own setting/ }).check()
    await again.getByRole('combobox').selectOption('5')
    await expect.poll(lockAfter).toBe(300)
    await again.getByRole('switch', { name: 'Lock when idle' }).click()
    await expect.poll(lockAfter).toBe(0)
    await again.getByRole('radio', { name: /Follow/ }).check()
    await expect.poll(lockAfter).toBe(desktopDelay)

    // This test's own login is in the audit log.
    await again.getByRole('button', { name: 'Recent Logins' }).click()
    await expect(
      again
        .getByRole('list', { name: 'Recent logins' })
        .getByText(/^logged in/)
        .first(),
    ).toBeVisible()
  } finally {
    await api.resetSettings()
  }
})
