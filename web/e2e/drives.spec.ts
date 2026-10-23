import { desktopIcon, expect, fileItem, password, test, windowNamed } from './fixtures'

// The label of a stick the user mounted through udisks (the e2e VM sets
// one up before each run; this test ejects it and mounts it again).
const usb = process.env.TILDE_USB

test('ejecting a USB stick, then mounting it with a password', async ({ desktop: page, home }) => {
  test.skip(!usb, 'set TILDE_USB to the label of a volume the user mounted through udisks')
  const user = home.split('/').pop()!

  await desktopIcon(page, 'Home').dblclick()
  const win = windowNamed(page, 'Home')
  const devices = win.getByRole('complementary')
  await devices.getByRole('button', { name: usb!, exact: true }).click()
  const stick = windowNamed(page, usb!)
  await expect(stick).toBeVisible()
  await expect(stick.getByRole('navigation', { name: 'Location' })).toContainText(usb!)

  // Eject from the sidebar: the window leaves the stick, and the user is
  // told it can be unplugged (powering off is polkit's call; unmounted is
  // what makes it safe).
  await stick.getByRole('button', { name: `Eject ${usb}` }).click()
  await expect(page.getByText(`“${usb}” can be safely removed.`)).toBeVisible()
  const computer = windowNamed(page, 'Computer')
  await expect(computer).toBeVisible()
  const item = fileItem(computer, usb!)
  await expect(item).toHaveClass(/hidden/) // dimmed: not mounted
  await expect(computer.getByRole('button', { name: `Eject ${usb}` })).toHaveCount(0)
  const mounted = await page.request.get(`/api/v1/fs/stat?path=${encodeURIComponent(`/media/${user}/${usb}`)}`)
  expect(mounted.status()).toBe(404)

  // Opening it mounts it. polkit wants a password for that from a web
  // session (the VM's rule asks for the user's own), and asks through
  // Tilde's agent (D44).
  await item.dblclick()
  const dialog = page.getByRole('alertdialog', { name: 'Authentication Required' })
  await expect(dialog).toContainText('Authentication is required to mount')
  await expect(dialog).toContainText(`Password for ${user}`)
  const entry = dialog.getByLabel('Password', { exact: true })
  await entry.fill('not-the-password')
  await dialog.getByRole('button', { name: 'Authenticate' }).click()
  await expect(dialog.getByRole('alert')).toBeVisible({ timeout: 15_000 })
  await expect(entry).toBeFocused()
  await entry.fill(password)
  await dialog.getByRole('button', { name: 'Authenticate' }).click()
  await expect(dialog).toBeHidden({ timeout: 15_000 })
  await expect(windowNamed(page, usb!)).toBeVisible()
  expect((await page.request.get(`/api/v1/fs/stat?path=${encodeURIComponent(`/media/${user}/${usb}`)}`)).status()).toBe(
    200,
  )
})

test('cancelling the password dialog leaves the stick unmounted', async ({ desktop: page, home, api }) => {
  test.skip(!usb, 'set TILDE_USB to the label of a volume the user mounted through udisks')
  const user = home.split('/').pop()!
  await api.unmount(usb!)

  await desktopIcon(page, 'Computer').dblclick()
  const computer = windowNamed(page, 'Computer')
  const mounted = page.waitForResponse((r) => r.url().endsWith('/api/v1/drives/mount'))
  await fileItem(computer, usb!).dblclick()
  const dialog = page.getByRole('alertdialog', { name: 'Authentication Required' })
  await expect(dialog).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  expect((await mounted).status()).toBe(403)
  // Cancelling is not an error worth a notice.
  await expect(page.getByText(/Could not mount/)).toHaveCount(0)
  await expect(fileItem(computer, usb!)).toHaveClass(/hidden/)
  expect((await page.request.get(`/api/v1/fs/stat?path=${encodeURIComponent(`/media/${user}/${usb}`)}`)).status()).toBe(
    404,
  )
})
