import { desktopIcon, expect, fileItem, png, test, windowNamed } from './fixtures'

// The image viewer: opened from Files, Best Fit (never enlarging), 100%,
// the folder's images in order, turning, Move to Trash, and a reload.
test('viewing a folder of images', async ({ desktop: page, api, scratch }) => {
  const folder = scratch.split('/').pop()!
  await api.write(`${scratch}/a.png`, png(400, 300))
  await api.write(`${scratch}/b.png`, png(40, 20, [60, 200, 60]))
  await api.write(`${scratch}/c.png`, png(3000, 1500, [60, 60, 200]))
  await api.write(`${scratch}/notes.txt`, 'not an image\n')

  await desktopIcon(page, 'Home').dblclick()
  await fileItem(windowNamed(page, 'Home'), folder).dblclick()
  await fileItem(windowNamed(page, folder), 'a.png').dblclick()

  const viewer = windowNamed(page, /— Image Viewer$/)
  const status = viewer.locator('.status')
  await expect(viewer).toHaveAccessibleName('a.png — Image Viewer')
  await expect(status).toContainText('400 × 300 pixels')
  await expect(status).toContainText('1 / 3')
  await expect(status).toContainText('100%')

  // The next image; a small one stays at its own size.
  await viewer.locator('.stage').click()
  await page.keyboard.press('ArrowRight')
  await expect(viewer).toHaveAccessibleName('b.png — Image Viewer')
  await expect(status).toContainText('40 × 20 pixels')
  await expect(status).toContainText('2 / 3')

  // A big one fits the window, then shows at 100% and turns.
  await page.keyboard.press('ArrowRight')
  await expect(viewer).toHaveAccessibleName('c.png — Image Viewer')
  await expect(status).toContainText('3000 × 1500 pixels')
  const bestFit = viewer.getByRole('button', { name: 'Best Fit' })
  await expect(bestFit).toHaveAttribute('aria-pressed', 'true')
  await expect(status).not.toContainText('100%')
  await page.keyboard.press('1')
  await expect(status).toContainText('100%')
  await expect(bestFit).toHaveAttribute('aria-pressed', 'false')
  await page.keyboard.press('Control+r')
  await expect(viewer.locator('.canvas img')).toHaveAttribute('style', /rotate\(90deg\)/)
  await page.keyboard.press('f')
  await expect(bestFit).toHaveAttribute('aria-pressed', 'true')

  // Around the end to the first.
  await page.keyboard.press('ArrowRight')
  await expect(viewer).toHaveAccessibleName('a.png — Image Viewer')
  await page.keyboard.press('ArrowLeft')
  await expect(viewer).toHaveAccessibleName('c.png — Image Viewer')

  // Move to Trash shows the image that takes its place.
  await viewer.getByRole('button', { name: 'Move to Trash' }).click()
  await expect(viewer).toHaveAccessibleName('b.png — Image Viewer')
  await expect(status).toContainText('2 / 2')
  expect(await api.exists(`${scratch}/c.png`)).toBe(false)

  // A reload brings the window back on the same image.
  await page.reload()
  await expect(viewer).toHaveAccessibleName('b.png — Image Viewer')
  await expect(status).toContainText('40 × 20 pixels')
})
