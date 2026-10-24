import { desktopIcon, expect, fileItem, password, test, windowNamed } from './fixtures'

// The text editor (D48): opened from Files, saved with Ctrl+S, and its
// unsaved changes kept by the worker through a reload and a lock.
test('editing a file: save, and unsaved changes survive a reload and a lock', async ({
  desktop: page,
  api,
  scratch,
}) => {
  const file = `${scratch}/notes.txt`
  await api.write(file, 'first line\n')
  const content = async () => (await page.request.get(`/api/v1/fs/content?path=${encodeURIComponent(file)}`)).text()

  await desktopIcon(page, 'Home').dblclick()
  await fileItem(windowNamed(page, 'Home'), scratch.split('/').pop()!).dblclick()
  await fileItem(windowNamed(page, scratch.split('/').pop()!), 'notes.txt').dblclick()

  const editor = windowNamed(page, /notes\.txt — Text Editor/)
  const text = editor.locator('.cm-content')
  await expect(text).toContainText('first line')
  await text.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('second line')
  await expect(editor).toHaveAccessibleName('*notes.txt — Text Editor')
  await page.keyboard.press('Control+s')
  await expect(editor).toHaveAccessibleName('notes.txt — Text Editor')
  expect(await content()).toBe('first line\nsecond line')

  // Unsaved: kept as a draft by the worker, not in the file.
  await page.keyboard.type(' and more')
  await expect(editor).toHaveAccessibleName('*notes.txt — Text Editor')
  await page.waitForTimeout(1000)
  await page.reload()
  await expect(text).toContainText('second line and more')
  await expect(editor).toHaveAccessibleName('*notes.txt — Text Editor')
  expect(await content()).toBe('first line\nsecond line')

  // Typed just before locking: written out before the lock.
  await text.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('!')
  await page.keyboard.press('Control+Alt+l')
  const lock = page.getByRole('main', { name: 'Locked' })
  await lock.getByLabel('Password').fill(password)
  await lock.getByRole('button', { name: 'Unlock' }).click()
  await expect(text).toContainText('second line and more!', { timeout: 10_000 })

  // Closing without saving asks, then forgets the changes.
  await editor.getByTitle('Close').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close Without Saving' }).click()
  await expect(editor).toHaveCount(0)
  const draft = await page.request.get(`/api/v1/drafts?key=${encodeURIComponent(`file:${file}`)}`)
  expect(draft.status()).toBe(404)
  expect(await content()).toBe('first line\nsecond line')
})
