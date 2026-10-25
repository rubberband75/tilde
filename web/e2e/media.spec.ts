import { readFileSync } from 'node:fs'
import { desktopIcon, expect, fileItem, test, wav, windowNamed } from './fixtures'

// The media player: opened from Files, it plays and moves on to the next
// file of the folder; the playlist opens a video; a paused position comes
// back after a reload (D51).
test('playing a folder of sound and video', async ({ desktop: page, api, scratch }) => {
  const folder = scratch.split('/').pop()!
  await api.write(`${scratch}/01 first.wav`, wav(2))
  await api.write(`${scratch}/02 second.wav`, wav(4, 660))
  await api.write(`${scratch}/clip.webm`, readFileSync(new URL('./media/clip.webm', import.meta.url)))
  await api.write(`${scratch}/notes.txt`, 'not media\n')

  await desktopIcon(page, 'Home').dblclick()
  await fileItem(windowNamed(page, 'Home'), folder).dblclick()
  await fileItem(windowNamed(page, folder), '01 first.wav').dblclick()

  const player = windowNamed(page, /— Media Player$/)
  const video = player.locator('video')
  const position = () => video.evaluate((v: HTMLVideoElement) => v.currentTime)
  await expect(player).toHaveAccessibleName('01 first.wav — Media Player')
  await expect(player.locator('.art')).toContainText('01 first.wav')
  await expect(player.getByLabel('Duration')).toHaveText('0:02')
  // Opened from Files, it plays at once.
  await expect(player.getByRole('button', { name: 'Pause' })).toBeVisible()

  // At the end it goes on to the next file of the folder.
  await expect(player).toHaveAccessibleName('02 second.wav — Media Player', { timeout: 10_000 })
  await expect(player.getByLabel('Duration')).toHaveText('0:04')
  await expect.poll(position).toBeGreaterThan(0.5)
  await player.getByRole('button', { name: 'Pause' }).click()

  // The playlist lists the folder's media; a video shows as a picture.
  await player.getByRole('button', { name: 'Playlist' }).click()
  const playlist = player.getByRole('complementary', { name: 'Playlist' })
  await expect(playlist.getByRole('button')).toHaveText(['01 first.wav', '02 second.wav', 'clip.webm'])
  await playlist.getByRole('button', { name: 'clip.webm' }).click()
  await expect(player).toHaveAccessibleName('clip.webm — Media Player')
  await expect(video).toBeVisible()
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.videoWidth)).toBe(160)

  // Paused partway, it comes back there after a reload, still paused.
  await expect.poll(position).toBeGreaterThan(1)
  await page.keyboard.press('Space')
  await expect(player.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
  const pausedAt = await position()
  await page.reload()
  await expect(player).toHaveAccessibleName('clip.webm — Media Player')
  await expect(player.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
  await expect.poll(position).toBeGreaterThan(pausedAt - 0.2)
  expect(await position()).toBeLessThan(pausedAt + 0.2)
})
