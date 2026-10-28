import { expect, test } from './fixtures'

test('the clock opens a calendar of this month', async ({ desktop: page }) => {
  // The browser's locale and time zone, as the clock uses.
  const { day, month, nextMonth } = await page.evaluate(() => {
    const now = new Date()
    const fmt = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'long' })
    return {
      day: String(now.getDate()),
      month: fmt.format(now),
      nextMonth: fmt.format(new Date(now.getFullYear(), now.getMonth() + 1, 1)),
    }
  })

  const clock = page.getByRole('navigation', { name: 'Panel' }).locator('.clock')
  await clock.click()
  // The popover is named after the clock that opened it.
  const calendar = page.getByRole('dialog').filter({ has: page.locator('.calendar') })
  await expect(calendar).toBeVisible()
  await expect(calendar.locator('td[aria-current="date"]')).toHaveText(day)
  const label = calendar.locator('.month-label')
  await expect(label).toHaveText(month)

  await calendar.getByRole('button', { name: 'Next month' }).click()
  await expect(label).toHaveText(nextMonth)
  await expect(calendar.locator('td[aria-current="date"]')).toHaveCount(0)
  await label.click()
  await expect(label).toHaveText(month)

  await page.keyboard.press('Escape')
  await expect(calendar).toHaveCount(0)
})
