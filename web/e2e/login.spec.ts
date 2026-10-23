import { expect, password, test, user } from './fixtures'

test('logging in, staying logged in across a reload, logging out', async ({ page }) => {
  await page.goto('/')
  const login = page.getByRole('button', { name: 'Log In' })

  await page.getByLabel('User name', { exact: true }).fill(user)
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

test('remembering the user name, and only the name', async ({ page }) => {
  await page.goto('/')
  const name = page.getByLabel('User name', { exact: true })
  const pass = page.getByLabel('Password')
  const remember = page.getByLabel('Remember user name')
  const logIn = async () => {
    await pass.fill(password)
    await page.getByRole('button', { name: 'Log In' }).click()
    await expect(page.getByRole('navigation', { name: 'Panel' })).toBeVisible({ timeout: 10_000 })
  }
  const logOut = async () => {
    await page.getByTitle('Menu').click()
    await page.getByTitle('Log Out').click()
    await expect(page.getByRole('button', { name: 'Log In' })).toBeVisible()
  }

  await expect(remember).not.toBeChecked()
  await name.fill(user)
  await remember.check()
  await logIn()
  const stored = await page.evaluate(() => JSON.stringify(localStorage))
  expect(stored).toContain(user)
  expect(stored).not.toContain(password)
  await logOut()

  // The name is filled in and the password field has the focus.
  await page.reload()
  await expect(name).toHaveValue(user)
  await expect(pass).toBeFocused()
  await expect(remember).toBeChecked()

  // Unticking forgets it at the next login.
  await remember.uncheck()
  await logIn()
  await logOut()
  await page.reload()
  await expect(name).toHaveValue('')
  await expect(remember).not.toBeChecked()
})
