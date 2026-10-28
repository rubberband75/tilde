import { desktopIcon, expect, test, windowNamed } from './fixtures'
import type { Locator, Page } from '@playwright/test'

/** Waits for a window's animation (D57) to end, so its box is its own. */
async function settled(l: Locator) {
  await expect.poll(() => l.evaluate((el) => el.getAnimations().length)).toBe(0)
}

async function box(l: Locator) {
  const b = (await l.boundingBox())!
  return { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height) }
}

/** Drags a window by its title bar, in steps as a hand would. */
async function dragTitle(page: Page, win: Locator, to: { x: number; y: number }, check?: () => Promise<void>) {
  const t = (await win.locator('.titlebar .title').boundingBox())!
  await page.mouse.move(t.x + t.width / 2, t.y + t.height / 2)
  await page.mouse.down()
  await page.mouse.move(to.x, to.y, { steps: 12 })
  await check?.()
  await page.mouse.up()
}

test('windows tile at the screen edges and keep their size for later', async ({ desktop: page }) => {
  const area = await box(page.locator('.window-layer'))
  await desktopIcon(page, 'Home').dblclick()
  const win = windowNamed(page, 'Home')
  await expect(win).toBeVisible()
  await settled(win)
  const before = await box(win)

  // The left edge offers the left half, shown while dragging.
  await dragTitle(page, win, { x: 0, y: area.h / 2 }, async () => {
    await expect(page.locator('.snap-preview')).toBeVisible()
  })
  await expect(page.locator('.snap-preview')).toHaveCount(0)
  await expect.poll(() => box(win)).toEqual({ x: 0, y: 0, w: Math.round(area.w / 2), h: area.h })

  // A corner offers a quarter.
  await dragTitle(page, win, { x: area.w - 1, y: area.h - 5 })
  await expect
    .poll(() => box(win))
    .toEqual({
      x: Math.round(area.w / 2),
      y: Math.round(area.h / 2),
      w: area.w - Math.round(area.w / 2),
      h: area.h - Math.round(area.h / 2),
    })

  // Dragged out of the tile, the window has its own size again.
  await dragTitle(page, win, { x: area.w / 2, y: area.h / 3 })
  await settled(win)
  const out = await box(win)
  expect({ w: out.w, h: out.h }).toEqual({ w: before.w, h: before.h })

  // The top edge offers the top half; a reload keeps the tile.
  const top = { x: 0, y: 0, w: area.w, h: Math.round(area.h / 2) }
  await dragTitle(page, win, { x: area.w / 2, y: 0 })
  await expect.poll(() => box(win)).toEqual(top)
  await page.reload()
  await expect(win).toHaveClass(/tiled/)
  await expect.poll(() => box(win)).toEqual(top)
})

test('the window menu: unmaximize, move, resize and always on top', async ({ desktop: page }) => {
  await desktopIcon(page, 'Home').dblclick()
  const win = windowNamed(page, 'Home')
  await expect(win).toBeVisible()
  const title = win.locator('.titlebar .title')
  const menuItem = (name: string) => page.getByRole('menuitem', { name, exact: true })
  const menu = async () => {
    await title.click({ button: 'right' })
    await expect(menuItem('Close')).toBeVisible()
  }

  await title.dblclick()
  await expect(win).toHaveClass(/maximized/)
  await menu()
  await expect(menuItem('Move')).toBeDisabled()
  await menuItem('Unmaximize').click()
  await expect(win).not.toHaveClass(/maximized/)
  await settled(win)
  const start = await box(win)

  // Move: the arrow keys move the window, Enter keeps it there.
  await menu()
  await menuItem('Move').click()
  for (let i = 0; i < 3; i++) await page.keyboard.press('ArrowRight')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect.poll(() => box(win)).toEqual({ ...start, x: start.x + 30, y: start.y + 10 })

  // Resize: Escape puts the size back.
  await menu()
  await menuItem('Resize').click()
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('ArrowDown')
  await expect.poll(async () => (await box(win)).w).toBe(start.w + 10)
  await page.keyboard.press('Escape')
  await expect.poll(() => box(win)).toEqual({ ...start, x: start.x + 30, y: start.y + 10 })

  // Always on Top keeps the window over one focused after it.
  await menu()
  await page.getByRole('menuitemcheckbox', { name: 'Always on Top' }).click()
  await desktopIcon(page, 'Computer').dblclick()
  const other = windowNamed(page, 'Computer')
  await expect(other).toBeVisible()
  const z = (l: Locator) => l.evaluate((el) => Number(getComputedStyle(el).zIndex))
  expect(await z(win)).toBeGreaterThan(await z(other))
  await menu()
  await expect(page.getByRole('menuitemcheckbox', { name: 'Always on Top' })).toHaveAttribute('aria-checked', 'true')
  await page.keyboard.press('Escape')

  // Close from the menu.
  await menu()
  await menuItem('Close').click()
  await expect(win).toHaveCount(0)
})

test('windows animate opening, closing, minimizing and maximizing, unless motion is reduced', async ({
  desktop: page,
}) => {
  await desktopIcon(page, 'Home').dblclick()
  const win = windowNamed(page, 'Home')
  await expect(win).toBeVisible()
  const running = () => win.evaluate((el) => el.getAnimations().length)
  // Opening plays; a reload brings the window back without it.
  expect(await running()).toBe(1)
  await settled(win)
  await page.reload()
  await expect(win).toBeVisible()
  expect(await running()).toBe(0)

  await win.locator('.titlebar .title').dblclick()
  await expect(win).toHaveClass(/maximized/)
  expect(await running()).toBe(1)

  // Into its window-list button, and hidden once that has played.
  await win.getByTitle('Minimize').click()
  expect(await running()).toBe(1)
  await expect(win).toBeHidden()
  await page.getByRole('toolbar', { name: 'Windows' }).getByTitle('Home').click()
  await expect(win).toBeVisible()
  await expect.poll(running).toBe(0)

  // Closing plays out, then the window goes.
  await win.getByTitle('Close').click()
  await expect(win).toHaveCount(0)

  await desktopIcon(page, 'Home').dblclick()
  await expect(win).toBeVisible()
  await settled(win)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await win.locator('.titlebar .title').dblclick()
  await expect(win).toHaveClass(/maximized/)
  expect(await running()).toBe(0)
  await win.getByTitle('Minimize').click()
  await expect(win).toBeHidden()
})
