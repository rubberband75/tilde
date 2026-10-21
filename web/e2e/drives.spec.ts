import { desktopIcon, expect, fileItem, test, windowNamed } from './fixtures'

// The label of a stick the user mounted through udisks (the e2e VM sets
// one up before each run; this test ejects it).
const usb = process.env.TILDE_USB

test('ejecting a USB stick, which then shows as not mounted', async ({ desktop: page, home }) => {
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

  // Mounting is not offered yet; opening it says how to mount it.
  await item.dblclick()
  await expect(page.getByText(/is not mounted\. .*udisksctl mount -b \/dev\/loop\d+/)).toBeVisible()
})
