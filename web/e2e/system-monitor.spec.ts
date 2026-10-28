import { expect, test, windowNamed } from './fixtures'

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
