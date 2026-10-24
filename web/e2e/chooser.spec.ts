import { expect, fileItem, test, windowNamed } from './fixtures'

// The shared file chooser (D49), through the text editor: Open lists the
// folder with a filter, Save As names a file in another folder and asks
// before replacing one. The editor waits, untouchable, while it is open.
test('the file chooser opens a file and saves copies elsewhere', async ({ desktop: page, api, scratch }) => {
  const folder = scratch.split('/').pop()!
  await api.write(`${scratch}/alpha.txt`, 'alpha\n')
  await api.write(`${scratch}/picture.png`, 'not really a picture')
  await api.mkdir(`${scratch}/sub`)
  const content = async (path: string) =>
    (await page.request.get(`/api/v1/fs/content?path=${encodeURIComponent(path)}`)).text()

  await page.getByTitle('Menu').click()
  await page.getByLabel('Search applications').fill('Text Editor')
  await page.keyboard.press('Enter')
  const untitled = windowNamed(page, 'Untitled Document — Text Editor')
  await expect(untitled).toBeVisible()

  // Open: typing a location goes to the folder; the filter hides the picture.
  await untitled.getByRole('button', { name: 'Open…' }).click()
  const open = windowNamed(page, 'Open File')
  await expect(open).toBeVisible()
  await expect(untitled.locator('.content')).toHaveAttribute('inert', '')
  await expect(open.getByRole('region', { name: 'Files' })).toBeFocused()
  await page.keyboard.type(`${scratch}/`)
  await expect(open.getByLabel('Location')).toHaveValue(`${scratch}/`)
  await page.keyboard.press('Enter')
  await expect(fileItem(open, 'alpha.txt')).toBeVisible()
  await expect(fileItem(open, 'sub')).toBeVisible()
  await expect(fileItem(open, 'picture.png')).toHaveCount(0)
  await open.getByLabel('Show files of type').selectOption('All Files')
  await expect(fileItem(open, 'picture.png')).toBeVisible()
  await fileItem(open, 'alpha.txt').dblclick()
  await expect(open).toHaveCount(0)

  // The untouched new document gave its place to the file.
  const editor = windowNamed(page, 'alpha.txt — Text Editor')
  const text = editor.locator('.cm-content')
  await expect(text).toContainText('alpha')
  await expect(untitled).toHaveCount(0)

  // Save As into a subfolder by typing its name with the file's.
  await text.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('beta')
  await page.keyboard.press('Control+Shift+s')
  const saveAs = windowNamed(page, 'Save As')
  const name = saveAs.getByLabel('Name:')
  await expect(name).toHaveValue('alpha.txt')
  await expect(saveAs.getByRole('button', { name: folder })).toHaveAttribute('aria-current', 'location')
  await expect(editor.locator('.content')).toHaveAttribute('inert', '')
  await name.fill('sub/copy.txt')
  await page.keyboard.press('Enter')
  await expect(saveAs).toHaveCount(0)
  const copy = windowNamed(page, 'copy.txt — Text Editor')
  await expect(copy).toBeVisible()
  expect(await content(`${scratch}/sub/copy.txt`)).toBe('alpha\nbeta')
  expect(await content(`${scratch}/alpha.txt`)).toBe('alpha\n')

  // Over an existing file: the chooser asks first.
  await page.keyboard.press('Control+Shift+s')
  await expect(saveAs.getByRole('button', { name: 'sub' })).toHaveAttribute('aria-current', 'location')
  await name.fill('../alpha.txt')
  await page.keyboard.press('Enter')
  const replace = page.getByRole('alertdialog')
  await expect(replace).toContainText('A file named “alpha.txt” already exists')
  await replace.getByRole('button', { name: 'Replace' }).click()
  await expect(windowNamed(page, 'alpha.txt — Text Editor')).toBeVisible()
  expect(await content(`${scratch}/alpha.txt`)).toBe('alpha\nbeta')

  // Escape cancels, and the editor takes keys again.
  await page.keyboard.press('Control+o')
  await expect(open).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(open).toHaveCount(0)
  await page.keyboard.type('!')
  await expect(windowNamed(page, '*alpha.txt — Text Editor')).toBeVisible()
})
