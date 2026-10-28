import { expect, password, test, windowNamed } from './fixtures'

// System Monitor (D59): every user's processes, and signals that only reach
// the user's own.
test('System Monitor lists every process and ends the user’s own', async ({ desktop: page }) => {
  // A process to end, started in a terminal.
  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  await expect(term.locator('.xterm-rows')).toContainText('$', { timeout: 10_000 })
  await term.locator('.xterm').click()
  await page.keyboard.type('sleep 4321\n')

  await page.keyboard.press('Alt+F1')
  await page.getByRole('searchbox', { name: 'Search applications' }).fill('System Monitor')
  await page.keyboard.press('Enter')
  const mon = windowNamed(page, 'System Monitor')
  // Launching from the menu's search closes the menu.
  await expect(page.getByRole('searchbox', { name: 'Search applications' })).toHaveCount(0)
  const table = mon.getByRole('table', { name: 'Processes' })
  const search = mon.getByRole('searchbox', { name: 'Search processes' })
  const row = (text: string) => table.getByRole('row').filter({ hasText: text })

  // All processes by default: root's are there, but not the user's to end.
  await expect(mon.getByRole('radio', { name: 'All Processes' })).toBeChecked()
  await search.fill('systemd')
  const systemd = row('root').first()
  await expect(systemd).toBeVisible()
  await systemd.click({ button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'End', exact: true })).toBeDisabled()
  await page.keyboard.press('Escape')
  await mon.getByRole('radio', { name: 'My Processes' }).click()
  await expect(row('root')).toHaveCount(0)

  // The sleep: found by its command line, ended after asking.
  await search.fill('4321')
  const sleep = row('sleep')
  await expect(sleep).toHaveCount(1)
  await sleep.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'End', exact: true }).click()
  const ask = page.getByRole('alertdialog', { name: /End the selected process “sleep”/ })
  await ask.getByRole('button', { name: 'End Process' }).click()
  await expect(sleep).toHaveCount(0)
  // The shell says so.
  await expect(term.locator('.xterm-rows')).toContainText('Terminated')
})

// D60: services that do nothing, which the e2e VM installs: a system one
// polkit lets the test user start with their own password, and a user one.
const service = process.env.TILDE_SERVICE
const userService = process.env.TILDE_USER_SERVICE

test('the Services tab starts and stops system and user services', async ({ desktop: page }) => {
  test.skip(!service || !userService, 'set TILDE_SERVICE and TILDE_USER_SERVICE (the e2e VM does)')
  test.setTimeout(90_000)
  await page.keyboard.press('Alt+F1')
  await page.getByRole('searchbox', { name: 'Search applications' }).fill('System Monitor')
  await page.keyboard.press('Enter')
  const mon = windowNamed(page, 'System Monitor')
  await mon.getByRole('tab', { name: 'Services' }).click()
  const table = mon.getByRole('table', { name: 'Services' })
  const search = mon.getByRole('searchbox', { name: 'Search services' })
  const row = (name: string) => table.getByRole('row').filter({ hasText: name })
  const state = (name: string) => row(name).getByRole('cell').nth(2)
  const button = (name: string) => mon.locator('footer').getByRole('button', { name, exact: true })

  // The system's services, and the test one stopped.
  await expect(mon.getByRole('radio', { name: 'System' })).toBeChecked()
  await search.fill('ssh')
  await expect(table.getByRole('row').nth(1)).toBeVisible()
  await search.fill(service!)
  await expect(state(service!)).toHaveText('Inactive')
  await row(service!).click()

  // Starting a system service asks polkit, which the VM's rule makes ask
  // for the user's own password, through Tilde's agent.
  const auth = page.getByRole('alertdialog', { name: 'Authentication Required' })
  for (const [action, after] of [
    ['Start', 'Running'],
    ['Stop', 'Inactive'],
  ] as const) {
    await button(action).click()
    await expect(auth).toBeVisible()
    await auth.getByLabel('Password', { exact: true }).fill(password)
    await auth.getByRole('button', { name: 'Authenticate' }).click()
    await expect(state(service!)).toHaveText(after, { timeout: 20_000 })
  }

  // The user's own: no password.
  await mon.getByRole('radio', { name: 'My Services' }).click()
  await search.fill(userService!)
  await expect(state(userService!)).toHaveText('Inactive')
  await row(userService!).click()
  await button('Start').click()
  await expect(state(userService!)).toHaveText('Running', { timeout: 20_000 })
  await button('Stop').click()
  await expect(state(userService!)).toHaveText('Inactive', { timeout: 20_000 })
  await expect(auth).toHaveCount(0)
})

// D61: the panel applet's graphs, and the Resources tab it opens.
test('the system monitor applet and the Resources tab', async ({ desktop: page }) => {
  const applet = page.getByRole('navigation', { name: 'Panel' }).getByRole('button', { name: /^CPU: / })
  // The first snapshot comes after a second of sampling.
  await expect(applet).toHaveAttribute('aria-label', /CPU: [\d.]+%, Memory: .+ of .+/, { timeout: 10_000 })
  await expect(applet.locator('polyline').first()).toHaveAttribute('points', /\d/)

  await applet.click()
  const mon = windowNamed(page, 'System Monitor')
  await expect(mon.getByRole('tab', { name: 'Resources' })).toHaveAttribute('aria-selected', 'true')
  await expect(mon.getByRole('list', { name: 'Each CPU' }).getByRole('listitem').first()).toContainText(
    /CPU1\s*[\d.]+%/,
  )
  await expect(mon.getByText(/Memory\s+.+ \([\d.]+%\) of .+/)).toBeVisible()

  // Hovering a graph reads the seconds out.
  const cpuGraph = mon.getByRole('img', { name: 'CPU use over the last minute' })
  const box = (await cpuGraph.boundingBox())!
  await page.mouse.move(box.x + box.width - 2, box.y + box.height / 2)
  await expect(mon.getByRole('status').filter({ hasText: 'now' })).toContainText(/CPU\s*[\d.]+%/)
})
