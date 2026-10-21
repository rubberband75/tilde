import { expect, password, test, user } from './fixtures'

test('logging in, staying logged in across a reload, logging out', async ({ page }) => {
  await page.goto('/')
  const login = page.getByRole('button', { name: 'Log In' })

  await page.getByLabel('User name').fill(user)
  await page.getByLabel('Password').fill(`wrong-${password}`)
  await login.click()
  // PAM delays failures by a couple of seconds.
  await expect(page.getByRole('alert')).toBeVisible({ timeout: 10_000 })

  await page.getByLabel('Password').fill(password)
  await login.click()
  const panel = page.getByRole('navigation', { name: 'Panel' })
  await expect(panel).toBeVisible({ timeout: 10_000 })
  await expect(panel.getByTitle(`Logged in as ${user}`)).toHaveText(user)

  // The session is a cookie, not page state.
  await page.reload()
  await expect(panel).toBeVisible()

  await page.getByTitle('Menu').click()
  await page.getByTitle('Log Out').click()
  await expect(login).toBeVisible()
  const res = await page.request.get('/api/v1/hello')
  expect(res.status()).toBe(401)
})
