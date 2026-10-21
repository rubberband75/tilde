import { desktopIcon, expect, test } from './fixtures'

test('desktop shows Computer, Home and Trash', async ({ desktop }) => {
  for (const name of ['Computer', 'Home', 'Trash']) await expect(desktopIcon(desktop, name)).toBeVisible()
})

test('desktop follows changes to the Desktop folder and the trash', async ({ desktop, api, home }) => {
  const dir = (await api.places()).find((p) => p.id === 'desktop')?.path ?? `${home}/Desktop`
  const name = `pw-desktop-${Date.now().toString(36)}`
  const trash = desktopIcon(desktop, 'Trash').locator('img').first()

  try {
    // Written behind the shell's back, so only the worker's inotify event can
    // show it. (The desktop used to poll every 10 s; this waits 5.)
    await api.mkdir(dir)
    await api.write(`${dir}/${name}.txt`, 'hello')
    const icon = desktopIcon(desktop, `${name}.txt`)
    await expect(icon).toBeVisible()

    // Delete moves it to the trash; the icon goes and the trash fills.
    await icon.click()
    await desktop.keyboard.press('Delete')
    await expect(icon).toHaveCount(0)
    await expect(trash).toHaveAttribute('src', /user-trash-full/)

    // Emptied elsewhere: the trash icon follows.
    const res = await desktop.request.post('/api/v1/trash/empty', {
      headers: { Origin: new URL(desktop.url()).origin },
    })
    expect(res.status()).toBe(204)
    await expect(trash).not.toHaveAttribute('src', /user-trash-full/)
  } finally {
    await api.remove(`${dir}/${name}.txt`)
  }
})

test('desktop icons stay where they are dragged', async ({ desktop: page, api, home }) => {
  const dir = (await api.places()).find((p) => p.id === 'desktop')?.path ?? `${home}/Desktop`
  const name = `pw-moved-${Date.now().toString(36)}.txt`
  await api.resetDesktopLayout()
  await api.mkdir(dir)
  await api.write(`${dir}/${name}`, 'x')
  const icon = desktopIcon(page, name)
  const area = page.locator('.desktop-icons')
  await expect(icon).toBeVisible()

  // Where the icon sits relative to the desktop.
  const offset = async () => {
    const [a, b] = await Promise.all([area.boundingBox(), icon.boundingBox()])
    return { x: Math.round(b!.x - a!.x), y: Math.round(b!.y - a!.y) }
  }
  expect((await offset()).x).toBeLessThan(50) // first column

  try {
    await icon.dragTo(area, { targetPosition: { x: 600, y: 400 } })
    // Snapped to the grid cell under the icon's new centre.
    await expect.poll(offset).toEqual({ x: 508, y: 392 })
    const moved = await offset()

    // Saved by the worker: still there after a reload.
    await page.reload()
    await expect(icon).toBeVisible()
    await expect.poll(offset).toEqual(moved)

    // Organize Desktop by Name puts it back in the flow.
    await area.click({ button: 'right', position: { x: 900, y: 100 } })
    await page.getByRole('menuitem', { name: 'Organize Desktop by Name' }).click()
    await expect.poll(async () => (await offset()).x).toBeLessThan(50)
  } finally {
    await api.remove(`${dir}/${name}`)
    await api.resetDesktopLayout()
  }
})
