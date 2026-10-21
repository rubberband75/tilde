import { desktopIcon, expect, fileItem, test, windowNamed } from './fixtures'

// A second filesystem the test user can write to (the e2e VM mounts one).
const drive = process.env.TILDE_DRIVE

test('files on another drive go to that drive’s trash', async ({ desktop: page, api }) => {
  test.skip(!drive, 'set TILDE_DRIVE to a writable mount point on another filesystem')
  const uid = ((await (await page.request.get('/api/v1/hello')).json()) as { uid: number }).uid
  const dir = `${drive}/pw-${Date.now().toString(36)}`
  const driveTrash = `${drive}/.Trash-${uid}/files/doc.txt`
  await api.mkdir(dir)
  await api.write(`${dir}/doc.txt`, 'on the drive')
  try {
    // Computer lists the drive; open it and the folder on it.
    await desktopIcon(page, 'Computer').dblclick()
    const win = windowNamed(page, 'Computer')
    const driveName = drive!.split('/').pop()!
    const dirName = dir.split('/').pop()!
    await fileItem(win, driveName).dblclick()
    await fileItem(windowNamed(page, driveName), dirName).dblclick()
    const folder = windowNamed(page, dirName)
    await fileItem(folder, 'doc.txt').click()
    await page.keyboard.press('Delete')
    await expect(fileItem(folder, 'doc.txt')).toHaveCount(0)
    // In the drive's own trash, not copied home; the desktop's trash fills.
    expect(await api.exists(driveTrash)).toBe(true)
    await expect(desktopIcon(page, 'Trash').locator('img').first()).toHaveAttribute('src', /user-trash-full/)

    // The Trash view lists it with the home trash, and restores it.
    await folder.getByRole('complementary').getByRole('button', { name: 'Trash' }).click()
    const trash = windowNamed(page, 'Trash')
    await fileItem(trash, 'doc.txt').click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Restore' }).click()
    await expect(fileItem(trash, 'doc.txt')).toHaveCount(0)
    expect(await api.exists(`${dir}/doc.txt`)).toBe(true)
    expect(await api.exists(driveTrash)).toBe(false)
  } finally {
    await api.remove(dir)
  }
})
