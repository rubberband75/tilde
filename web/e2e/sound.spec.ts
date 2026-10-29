import { expect, test, windowNamed } from './fixtures'

// The sound applet (D79): the host's outputs through its sound server
// (PipeWire in the VM, with a dummy output and one this test adds).
test('the sound applet: choosing an output, its volume, and following the server', async ({ desktop: page }) => {
  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  const screen = term.locator('.xterm-rows')
  await expect(screen).toContainText('$', { timeout: 10_000 })
  const type = async (line: string) => {
    await term.locator('.xterm').click()
    await page.keyboard.type(line + '\n')
  }
  // Two outputs of the test's own (PipeWire's dummy one goes once there
  // is a real one), none left from an earlier run.
  const unload = 'pactl unload-module module-null-sink 2>/dev/null'
  await type(
    `${unload}; pactl load-module module-null-sink sink_name=tilde_a sink_properties=device.description=Tilde-A >/dev/null && ` +
      'pactl load-module module-null-sink sink_name=tilde_b sink_properties=device.description=Tilde-B >/dev/null && ' +
      'pactl set-default-sink tilde_a && echo loaded-$((1 + 1))',
  )
  await expect(screen).toContainText('loaded-2')

  try {
    const applet = page.getByRole('button', { name: /^Volume: / })
    await expect(applet).toHaveAttribute('aria-label', /\(Tilde-A\)$/)
    await applet.click()
    const output = page.getByRole('radio', { name: 'Tilde-B' })
    await output.click()
    await expect(output).toHaveAttribute('aria-checked', 'true')
    // The applet shows it at once; the server hears of it a moment later.
    const sent = page.waitForResponse(
      (r) =>
        r.url().includes('/api/v1/sound') &&
        r.request().method() === 'POST' &&
        !!r.request().postData()?.includes('"volume":35'),
    )
    await page.getByRole('slider', { name: 'Volume', exact: true }).fill('35')
    await sent
    await expect(applet).toHaveAttribute('aria-label', 'Volume: 35% (Tilde-B)')
    await page.keyboard.press('Escape')

    // The server has it; and a change made elsewhere shows in the applet.
    await type(
      'echo "vol=$(pactl get-sink-volume tilde_b | grep -o "[0-9]*%" | head -1) def=$(pactl get-default-sink)"',
    )
    await expect(screen).toContainText('vol=35% def=tilde_b')
    await type('pactl set-sink-volume tilde_b 20% && pactl set-sink-mute tilde_b 1')
    await expect(applet).toHaveAttribute('aria-label', 'Volume: muted (Tilde-B)')
  } finally {
    await type(unload)
  }
  await term.getByTitle('Close').click()
  await expect(term).toHaveCount(0)
})
