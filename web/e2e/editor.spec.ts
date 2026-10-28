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

// D58: drafts whose windows are gone (another browser, cleared storage)
// are offered in a new empty editor window.
test('recovering unsaved documents whose windows are gone', async ({ desktop: page, scratch }) => {
  const origin = new URL(page.url()).origin
  const draftUrl = (key: string) => `/api/v1/drafts?key=${encodeURIComponent(key)}`
  const untitled = `new:pw-${Date.now().toString(36)}`
  const file = `${scratch}/lost.txt`
  // Only this test's drafts, so the list empties when they are dealt with.
  const leftovers = (await (await page.request.get('/api/v1/drafts')).json()) as { drafts: { key: string }[] }
  for (const d of leftovers.drafts) await page.request.delete(draftUrl(d.key), { headers: { Origin: origin } })
  for (const [key, text] of [
    [untitled, '\n  Shopping list\nmilk\n'],
    [`file:${file}`, 'never written'],
  ]) {
    const res = await page.request.put(draftUrl(key!), { data: { text }, headers: { Origin: origin } })
    expect(res.status()).toBe(204)
  }

  await page.keyboard.press('Alt+F1')
  await page.getByRole('searchbox', { name: 'Search applications' }).fill('Text Editor')
  await page.keyboard.press('Enter')
  // Exactly: the recovered window's name has a "*" in front.
  const blank = page.getByRole('dialog', { name: 'Untitled Document — Text Editor', exact: true })
  await expect(blank.getByRole('status')).toContainText(/unsaved documents? can be recovered/)
  await blank.getByRole('button', { name: 'Show' }).click()
  const list = blank.getByRole('list', { name: 'Unsaved documents' })
  const lost = list.getByRole('listitem').filter({ hasText: 'lost.txt' })
  const shopping = list.getByRole('listitem').filter({ hasText: 'Shopping list' })
  await expect(lost).toContainText(scratch)
  await expect(shopping).toContainText('Never saved')

  // Discarding asks, and deletes the draft.
  await lost.getByRole('button', { name: 'Discard…' }).click()
  await page.getByRole('alertdialog', { name: 'Discard “lost.txt”?' }).getByRole('button', { name: 'Discard' }).click()
  await expect(lost).toHaveCount(0)
  expect((await page.request.get(draftUrl(`file:${file}`))).status()).toBe(404)

  // Opening brings it back in its own window, still unsaved.
  await shopping.getByRole('button', { name: 'Open' }).click()
  const back = page
    .getByRole('dialog', { name: '*Untitled Document — Text Editor' })
    .filter({ has: page.locator('.cm-content', { hasText: 'Shopping list' }) })
  await expect(back).toBeVisible()
  // The list is empty now: it closes, and the empty window it was in goes.
  await expect(blank).toHaveCount(0)

  await back.getByTitle('Close').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close Without Saving' }).click()
  await expect(back).toHaveCount(0)
  expect((await page.request.get(draftUrl(untitled))).status()).toBe(404)
})
