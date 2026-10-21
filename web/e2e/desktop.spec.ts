import { desktopIcon, expect, test } from './fixtures'

test('desktop shows Computer, Home and Trash', async ({ desktop }) => {
  for (const name of ['Computer', 'Home', 'Trash']) await expect(desktopIcon(desktop, name)).toBeVisible()
})

test('desktop follows changes to the Desktop folder and the trash', async ({ desktop, api, home }) => {
  const dir = (await api.places()).find((p) => p.id === 'desktop')?.path ?? `${home}/Desktop`
  const name = `pw-desktop-${Date.now().toString(36)}`
  const trash = desktopIcon(desktop, 'Trash').locator('img').first()

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
  const res = await desktop.request.post('/api/v1/trash/empty', { headers: { Origin: new URL(desktop.url()).origin } })
  expect(res.status()).toBe(204)
  await expect(trash).not.toHaveAttribute('src', /user-trash-full/)
})
