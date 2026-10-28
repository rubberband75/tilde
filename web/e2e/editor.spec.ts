import { desktopIcon, expect, fileItem, password, png, test, windowNamed } from './fixtures'

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
  await editor.getByTitle('Close', { exact: true }).click()
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

  await back.getByTitle('Close', { exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close Without Saving' }).click()
  await expect(back).toHaveCount(0)
  expect((await page.request.get(draftUrl(untitled))).status()).toBe(404)
})

// D63: files open as tabs of the last editor window, as in xed.
test('editor tabs: open, switch, close, move to a window, and back after a reload', async ({
  desktop: page,
  api,
  scratch,
}) => {
  const folder = scratch.split('/').pop()!
  await api.write(`${scratch}/a.txt`, 'alpha\n')
  await api.write(`${scratch}/b.txt`, 'bravo\n')
  await desktopIcon(page, 'Home').dblclick()
  await fileItem(windowNamed(page, 'Home'), folder).dblclick()
  const files = windowNamed(page, folder)
  await fileItem(files, 'a.txt').dblclick()
  const editor = page.getByRole('dialog', { name: /— Text Editor$/ })
  await expect(editor).toHaveAccessibleName('a.txt — Text Editor')

  // The second file: a tab of the same window. (Files first comes back
  // to the front: the editor opened over it.)
  await page.getByRole('toolbar', { name: 'Windows' }).getByTitle(folder, { exact: true }).click()
  await fileItem(files, 'b.txt').dblclick()
  await expect(editor).toHaveCount(1)
  await expect(editor).toHaveAccessibleName('b.txt — Text Editor')
  const tabs = editor.getByRole('tablist', { name: 'Documents' }).getByRole('tab')
  await expect(tabs).toHaveText([/a\.txt/, /b\.txt/])
  const text = editor.locator('.cm-content:visible')
  await expect(text).toContainText('bravo')

  // Switching: Ctrl+PgUp, Alt+number.
  await page.keyboard.press('Control+PageUp')
  await expect(editor).toHaveAccessibleName('a.txt — Text Editor')
  await expect(text).toContainText('alpha')
  await page.keyboard.press('Alt+2')
  await expect(editor).toHaveAccessibleName('b.txt — Text Editor')

  // Unsaved: a dot on the tab; closing it asks.
  await text.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('more')
  await expect(tabs.nth(1).getByLabel('unsaved')).toBeVisible()
  await page.keyboard.press('Control+Alt+w')
  const ask = page.getByRole('alertdialog', { name: 'Close “b.txt” without saving?' })
  await ask.getByRole('button', { name: 'Cancel' }).click()
  await expect(tabs).toHaveCount(2)

  // A new tab, then a.txt into a window of its own.
  await text.click()
  await page.keyboard.press('Control+Alt+n')
  await expect(tabs).toHaveCount(3)
  await expect(editor.first()).toHaveAccessibleName('Untitled Document — Text Editor')
  await tabs.nth(0).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Move to New Window' }).click()
  await expect(page.getByRole('dialog', { name: 'a.txt — Text Editor' })).toBeVisible()
  const rest = page.getByRole('dialog', { name: 'Untitled Document — Text Editor', exact: true })
  await expect(rest.getByRole('tab')).toHaveText([/b\.txt/, /Untitled Document/])

  // A reload brings both windows back, tabs and unsaved changes and all.
  await page.waitForTimeout(1000)
  await page.reload()
  await expect(rest.getByRole('tab')).toHaveText([/b\.txt.*●|b\.txt/, /Untitled Document/])
  await expect(rest.getByRole('tab').nth(0).getByLabel('unsaved')).toBeVisible()
  await expect(page.getByRole('dialog', { name: 'a.txt — Text Editor' })).toBeVisible()

  // Clean up: closing the window asks once for its unsaved document.
  await rest.getByRole('button', { name: 'Close', exact: true }).click()
  await page
    .getByRole('alertdialog', { name: 'Close “b.txt” without saving?' })
    .getByRole('button', { name: 'Close Without Saving' })
    .click()
  await expect(rest).toHaveCount(0)
})

// Dragging files into the editor opens them as tabs; a tab dropped on the
// desktop (which must not take it) goes to a window of its own.
test('dragging text files into the editor, and a tab onto the desktop', async ({ desktop: page, api, scratch }) => {
  const folder = scratch.split('/').pop()!
  await api.write(`${scratch}/first.txt`, 'first\n')
  await api.write(`${scratch}/second.txt`, 'second\n')
  await api.write(`${scratch}/pic.png`, png(4, 4))
  await desktopIcon(page, 'Home').dblclick()
  await fileItem(windowNamed(page, 'Home'), folder).dblclick()
  const files = windowNamed(page, folder)
  await fileItem(files, 'first.txt').dblclick()
  const editor = page.getByRole('dialog', { name: /— Text Editor$/ })
  await expect(editor).toHaveAccessibleName('first.txt — Text Editor')

  // Side by side, so both can be seen.
  await page.keyboard.press('Alt+Shift+ArrowRight')
  await page.getByRole('toolbar', { name: 'Windows' }).getByTitle(folder, { exact: true }).click()
  await page.keyboard.press('Alt+Shift+ArrowLeft')
  await expect.poll(() => editor.evaluate((el) => el.getAnimations().length)).toBe(0)

  // A text file: a tab, not its path pasted into the text.
  await fileItem(files, 'second.txt').dragTo(editor.locator('.cm-content:visible'))
  await expect(editor).toHaveAccessibleName('second.txt — Text Editor')
  const tabs = editor.getByRole('tab')
  await expect(tabs).toHaveText([/first\.txt/, /second\.txt/])
  await tabs.nth(0).click()
  await expect(editor.locator('.cm-content:visible')).toHaveText('first')

  // Not a text file: said so, and nothing opens.
  await fileItem(files, 'pic.png').dragTo(editor.locator('.cm-content:visible'))
  await expect(page.getByText('“pic.png” is not a text file.')).toBeVisible()
  await expect(tabs).toHaveCount(2)

  // A tab onto the desktop: its own window.
  await files.getByTitle('Minimize').click()
  await expect(files).toBeHidden()
  await tabs.nth(1).dragTo(page.locator('.desktop-icons'), { targetPosition: { x: 400, y: 400 } })
  const second = page.getByRole('dialog', { name: 'second.txt — Text Editor' })
  const first = page.getByRole('dialog', { name: 'first.txt — Text Editor' })
  await expect(second).toBeVisible()
  await expect(first).toBeVisible()
  // A single document still has its tab, so it can be dragged too.
  await expect(first.getByRole('tab')).toHaveCount(1)

  // first.txt's only tab into second.txt's window: the tab moves, and the
  // window it leaves empty closes.
  await page.keyboard.press('Alt+Shift+ArrowLeft') // second.txt, just opened, to the left
  await expect.poll(() => second.evaluate((el) => el.getAnimations().length)).toBe(0)
  await first.getByRole('tab').dragTo(second.getByRole('tab'), { targetPosition: { x: 150, y: 10 } })
  // One editor window left, now showing the tab it took (so named for it).
  const both = page.getByRole('dialog', { name: /— Text Editor$/ })
  await expect(both).toHaveCount(1)
  await expect(both).toHaveAccessibleName('first.txt — Text Editor')
  await expect(both.getByRole('tab')).toHaveText([/second\.txt/, /first\.txt/])
  await expect(both.locator('.cm-content:visible')).toHaveText('first')
})
