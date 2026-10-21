import { desktopIcon, expect, fileItem, test, windowNamed } from './fixtures'

test('Files: browse, new folder, live updates, trash and restore', async ({ desktop: page, api, scratch }) => {
  const scratchName = scratch.split('/').pop()!

  await desktopIcon(page, 'Home').dblclick()
  const home = windowNamed(page, 'Home')
  await expect(home).toBeVisible()
  await fileItem(home, scratchName).dblclick()
  const win = windowNamed(page, scratchName)
  await expect(win).toBeVisible()
  await expect(win.getByText('0 items')).toBeVisible()

  // New Folder starts an inline rename.
  await win.locator('.view').click({ button: 'right', position: { x: 300, y: 200 } })
  await page.getByRole('menuitem', { name: 'New Folder' }).click()
  const rename = win.locator('input.rename')
  await expect(rename).toBeFocused()
  await rename.fill('Projects')
  await rename.press('Enter')
  await expect(fileItem(win, 'Projects')).toBeVisible()
  expect(await api.exists(`${scratch}/Projects`)).toBe(true)

  // Changes made elsewhere arrive through the worker's inotify events.
  await api.write(`${scratch}/notes.txt`, 'notes')
  await expect(fileItem(win, 'notes.txt')).toBeVisible()
  await api.remove(`${scratch}/Projects`)
  await expect(fileItem(win, 'Projects')).toHaveCount(0)

  // Delete moves to the trash; restore brings it back.
  await fileItem(win, 'notes.txt').click()
  await page.keyboard.press('Delete')
  await expect(fileItem(win, 'notes.txt')).toHaveCount(0)

  await win.getByRole('button', { name: 'Trash' }).click()
  // The window is titled after the folder it shows.
  const trash = windowNamed(page, 'Trash')
  await expect(trash.getByRole('complementary').getByRole('button', { name: 'Trash' })).toHaveClass(/active/)
  const trashed = fileItem(trash, 'notes.txt')
  await trashed.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Restore' }).click()
  await expect(trashed).toHaveCount(0)
  expect(await api.exists(`${scratch}/notes.txt`)).toBe(true)
})
