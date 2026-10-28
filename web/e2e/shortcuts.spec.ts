import { desktopIcon, expect, test, windowNamed } from './fixtures'
import type { Locator } from '@playwright/test'

async function box(l: Locator) {
  const b = (await l.boundingBox())!
  return { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height) }
}

test('keyboard shortcuts: tiling, switching, Show Desktop, menus, close', async ({ desktop: page }) => {
  const area = await box(page.locator('.window-layer'))
  const halfW = Math.round(area.w / 2)
  const halfH = Math.round(area.h / 2)

  await desktopIcon(page, 'Home').dblclick()
  const home = windowNamed(page, 'Home')
  await expect(home).toHaveClass(/focused/)

  // Alt+Shift stands in for Super; Super works where it reaches the page.
  await page.keyboard.press('Alt+Shift+ArrowLeft')
  await expect.poll(() => box(home)).toEqual({ x: 0, y: 0, w: halfW, h: area.h })
  await page.keyboard.press('Meta+ArrowRight')
  await page.keyboard.press('Meta+ArrowRight')
  await expect.poll(() => box(home)).toEqual({ x: halfW, y: 0, w: area.w - halfW, h: area.h })
  await page.keyboard.press('Alt+Shift+ArrowUp')
  await expect.poll(() => box(home)).toEqual({ x: halfW, y: 0, w: area.w - halfW, h: halfH })

  await desktopIcon(page, 'Computer').dblclick()
  const computer = windowNamed(page, 'Computer')
  await expect(computer).toHaveClass(/focused/)

  // Alt+`: the switcher shows while Alt is held; letting go picks the window.
  await page.keyboard.down('Alt')
  await page.keyboard.press('Backquote')
  const switcher = page.getByRole('listbox', { name: 'Windows' })
  await expect(switcher).toBeVisible()
  await expect(switcher.getByRole('option', { selected: true })).toHaveAttribute('title', 'Home')
  await page.keyboard.up('Alt')
  await expect(switcher).toHaveCount(0)
  await expect(home).toHaveClass(/focused/)
  // Alt+Tab, as it comes in fullscreen with Keyboard Lock.
  await page.keyboard.down('Alt')
  await page.keyboard.press('Tab')
  await page.keyboard.up('Alt')
  await expect(computer).toHaveClass(/focused/)

  // Show Desktop hides everything, and the second press brings it back.
  await page.keyboard.press('Alt+Shift+KeyD')
  await expect(home).toBeHidden()
  await expect(computer).toBeHidden()
  await page.keyboard.press('Alt+Shift+KeyD')
  await expect(home).toBeVisible()
  await expect(computer).toHaveClass(/focused/)

  // Alt+F1 opens the start menu.
  await page.keyboard.press('Alt+F1')
  const search = page.getByRole('searchbox', { name: 'Search applications' })
  await expect(search).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(search).toHaveCount(0)

  // Alt+Shift+Space opens the window menu, ready for the keyboard.
  await page.keyboard.press('Alt+Shift+Space')
  const close = page.getByRole('menuitem', { name: 'Close', exact: true })
  await expect(close).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(close).toHaveCount(0)

  // Alt+Shift+F4 closes the focused window.
  await page.keyboard.press('Alt+Shift+F4')
  await expect(computer).toHaveCount(0)
  await expect(home).toHaveClass(/focused/)
})

test('shortcuts never reach a terminal', async ({ desktop: page }) => {
  await page.getByRole('navigation', { name: 'Panel' }).getByTitle('Terminal').click()
  const term = windowNamed(page, /Terminal|@/)
  const screen = term.locator('.xterm-rows')
  await expect(screen).toContainText('$', { timeout: 10_000 })
  await term.locator('.xterm').click()
  await page.keyboard.type('echo one')
  // In the shell these would be an escape sequence moving the cursor back a word.
  await page.keyboard.press('Alt+Shift+ArrowLeft')
  await page.keyboard.press('Alt+Shift+ArrowRight')
  await page.keyboard.type(' two-$((6*7))\n')
  await expect(screen).toContainText('one two-42')
})

test('fullscreen from the panel', async ({ desktop: page }) => {
  const button = page.getByRole('navigation', { name: 'Panel' }).getByTitle(/Fullscreen/)
  await button.click()
  await expect.poll(() => page.evaluate(() => document.fullscreenElement === document.documentElement)).toBe(true)
  await expect(button).toHaveAttribute('title', 'Leave Fullscreen')
  await button.click()
  await expect.poll(() => page.evaluate(() => document.fullscreenElement)).toBeNull()
})
