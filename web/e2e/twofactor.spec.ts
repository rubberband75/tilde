import { expect, password, test, totpCodes, twoFactorUser } from './fixtures'

// D43: PAM's code question after the password, on the login screen and the
// lock screen.
test('logging in and unlocking with a verification code', async ({ page }) => {
  test.skip(!twoFactorUser, 'set TILDE_2FA_USER and TILDE_2FA_SECRET')
  const code = totpCodes()
  await page.goto('/')
  const panel = page.getByRole('navigation', { name: 'Panel' })
  const answer = page.locator('#auth-answer')

  await page.getByLabel('User name', { exact: true }).fill(twoFactorUser)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Log In' }).click()
  // PAM's own question, with a hidden field.
  await expect(answer).toBeVisible({ timeout: 10_000 })
  await expect(answer).toHaveAttribute('type', 'password')
  await expect(page.locator('label[for="auth-answer"]')).not.toBeEmpty()

  // A wrong code goes back to the password.
  await answer.fill('000000')
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByRole('alert')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByLabel('Password')).toBeVisible()

  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Log In' }).click()
  await answer.fill(code())
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(panel).toBeVisible({ timeout: 10_000 })

  // Unlocking asks for a code too.
  await page.getByTitle('Menu').click()
  await page.getByTitle('Lock Screen').click()
  const lock = page.getByRole('main', { name: 'Locked' })
  await lock.getByLabel('Password').fill(password)
  await lock.getByRole('button', { name: 'Unlock' }).click()
  await expect(answer).toBeVisible({ timeout: 10_000 })
  await answer.fill(code())
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(panel).toBeVisible({ timeout: 10_000 })

  await page.getByTitle('Menu').click()
  await page.getByTitle('Log Out').click()
  await expect(page.getByRole('button', { name: 'Log In' })).toBeVisible()
})
