import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useFileChooser } from './fileChooser'
import { useWindows } from './windows'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
})

describe('file chooser', () => {
  it('answers the app that asked, and the next one starts in the same folder', async () => {
    const wm = useWindows()
    const chooser = useFileChooser()
    const editor = wm.open('text-editor')!
    const answer = chooser.choose(editor, { mode: 'save', name: 'a.txt' })
    const dialog = wm.dialogOf(editor)!
    expect(dialog.appId).toBe('file-chooser')
    expect(dialog.args.options).toEqual({ mode: 'save', name: 'a.txt' })
    chooser.answer(dialog.id, { paths: ['/home/me/notes/a.txt'], replace: false }, '/home/me/notes')
    await expect(answer).resolves.toEqual({ paths: ['/home/me/notes/a.txt'], replace: false })
    expect(wm.dialogOf(editor)).toBeUndefined()
    expect(chooser.lastFolder).toBe('/home/me/notes')
  })

  it('answers null when the chooser or its window closes, or on logout', async () => {
    const wm = useWindows()
    const chooser = useFileChooser()
    const editor = wm.open('text-editor')!
    const first = chooser.choose(editor, { mode: 'open' })
    wm.close(wm.dialogOf(editor)!.id)
    await expect(first).resolves.toBeNull()

    const second = chooser.choose(editor, { mode: 'open' })
    // One chooser per window at a time.
    await expect(chooser.choose(editor, { mode: 'open' })).resolves.toBeNull()
    wm.close(editor)
    await expect(second).resolves.toBeNull()
    expect(wm.windows).toHaveLength(0)

    const other = wm.open('text-editor')!
    const third = chooser.choose(other, { mode: 'folder' })
    wm.reset()
    chooser.reset()
    await expect(third).resolves.toBeNull()
  })
})
