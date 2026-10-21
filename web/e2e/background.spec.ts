import { expect, test, windowNamed } from './fixtures'

// A 2×2 PNG, so the picture is a real image the browser can draw.
const png = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEklEQVR4nGP4z8DA0PCfAUIBACFyBP1TVaZ3AAAAAElFTkSuQmCC',
  'base64',
)

test('picking a desktop background, and going back to Cinnamon’s', async ({ desktop: page, api, home, scratch }) => {
  const folders = `${home}/.cinnamon/backgrounds/user-folders.lst`
  const picture = `${scratch}/tilde-test.png`
  const style = () => page.locator('.desktop').evaluate((el: HTMLElement) => el.style.backgroundImage)
  await api.resetBackground()
  // A folder added in Cinnamon's Backgrounds settings is a collection.
  await api.write(picture, png)
  await api.mkdir(`${home}/.cinnamon`)
  await api.mkdir(`${home}/.cinnamon/backgrounds`)
  await api.write(folders, `${scratch}\n`)
  try {
    await page.reload()
    await page.locator('.desktop-icons').click({ button: 'right', position: { x: 900, y: 100 } })
    await page.getByRole('menuitem', { name: 'Change Desktop Background' }).click()
    const win = windowNamed(page, 'Backgrounds')
    await win.getByRole('button', { name: scratch.split('/').pop()! }).click()
    await win.getByRole('option', { name: 'tilde-test.png' }).click()
    await expect(win.getByRole('option', { name: 'tilde-test.png' })).toHaveAttribute('aria-selected', 'true')
    await expect.poll(style).toContain(encodeURIComponent(picture))

    await win.getByLabel('Picture aspect').selectOption('Mosaic')
    await expect
      .poll(() => page.locator('.desktop').evaluate((el: HTMLElement) => el.style.backgroundRepeat))
      .toBe('repeat')

    // Saved by the worker: still there after a reload.
    await page.reload()
    await expect.poll(style).toContain(encodeURIComponent(picture))

    await page.locator('.desktop-icons').click({ button: 'right', position: { x: 900, y: 100 } })
    await page.getByRole('menuitem', { name: 'Change Desktop Background' }).click()
    await windowNamed(page, 'Backgrounds').getByRole('button', { name: 'Use Cinnamon’s Background' }).click()
    await expect.poll(style).not.toContain(encodeURIComponent(picture))
  } finally {
    await api.remove(folders)
    await api.resetBackground()
  }
})
