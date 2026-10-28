import { expect, password, test } from './fixtures'
import type { Page } from '@playwright/test'

// Wi-Fi networks the e2e VM serves with hostapd on mac80211_hwsim radios;
// NetworkManager manages one more radio, the device the applet shows (D55).
const secure = process.env.TILDE_WIFI_SECURE
const open = process.env.TILDE_WIFI_OPEN
const wifiPass = process.env.TILDE_WIFI_PASS

function applet(page: Page) {
  const trigger = page.getByRole('navigation', { name: 'Panel' }).getByRole('button', { name: /^Network:/ })
  const wifi = page.getByRole('region', { name: 'Wi-Fi' })
  return {
    trigger,
    wifi,
    networks: wifi.getByRole('list', { name: 'Wi-Fi networks' }),
    network: (ssid: string) => wifi.getByRole('button', { name: new RegExp(`^${ssid}\\b`) }),
    async open() {
      if (!(await wifi.isVisible())) await trigger.click()
      await expect(wifi).toBeVisible()
    },
  }
}

test('the network applet connects to Wi-Fi networks and turns Wi-Fi off', async ({ desktop: page }) => {
  test.skip(
    !secure || !open || !wifiPass,
    'set TILDE_WIFI_SECURE, TILDE_WIFI_OPEN and TILDE_WIFI_PASS (the e2e VM does)',
  )
  test.setTimeout(150_000)
  const a = applet(page)

  await expect(a.trigger).toBeVisible()
  await a.open()
  // Opening the applet asks for a scan; the networks come in its events.
  await expect(a.network(open!)).toBeVisible({ timeout: 30_000 })
  await expect(a.network(secure!)).toBeVisible()

  // An open network: no password at all.
  await a.network(open!).click()
  await expect(a.trigger).toHaveAttribute('title', `Connected to ${open}`, { timeout: 30_000 })
  await a.open()
  await expect(a.network(open!)).toHaveAttribute('aria-current', 'true')

  // A secured one asks for its password, and again when it was wrong.
  await a.network(secure!).click()
  const ask = page.getByRole('alertdialog', { name: `Connect to “${secure}”` })
  await expect(ask).toContainText('This Wi-Fi network needs a password.')
  await ask.getByLabel('Password').fill('not-the-password')
  await ask.getByRole('button', { name: 'Connect' }).click()
  await expect(ask).toContainText('The password was not accepted.', { timeout: 60_000 })
  await expect(ask.getByLabel('Password')).toHaveAttribute('type', 'password')
  await ask.getByLabel('Password').fill(wifiPass!)
  await ask.getByRole('button', { name: 'Connect' }).click()
  await expect(ask).toBeHidden()
  await expect(a.trigger).toHaveAttribute('title', `Connected to ${secure}`, { timeout: 30_000 })

  // Disconnect: the browser comes in on loopback here, so nothing to warn about.
  await a.open()
  await a.wifi.getByRole('button', { name: 'Disconnect' }).click()
  await expect(a.trigger).toHaveAttribute('title', 'Not connected', { timeout: 15_000 })

  // Wi-Fi off asks polkit, which the VM's rule makes ask for the user's
  // own password, through Tilde's agent (D44).
  await a.open()
  const toggle = a.wifi.getByRole('switch', { name: 'Wi-Fi' })
  await expect(toggle).toBeChecked()
  await toggle.click()
  const auth = page.getByRole('alertdialog', { name: 'Authentication Required' })
  await expect(auth).toBeVisible()
  await auth.getByLabel('Password', { exact: true }).fill(password)
  await auth.getByRole('button', { name: 'Authenticate' }).click()
  await expect(auth).toBeHidden({ timeout: 15_000 })
  await a.open()
  await expect(toggle).not.toBeChecked({ timeout: 15_000 })
  await expect(a.networks).toHaveCount(0)

  await toggle.click()
  await auth.getByLabel('Password', { exact: true }).fill(password)
  await auth.getByRole('button', { name: 'Authenticate' }).click()
  await a.open()
  await expect(toggle).toBeChecked({ timeout: 15_000 })
})
