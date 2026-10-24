import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useWindows } from './windows'

/** Which files the chooser lists: by MIME type ("text/*" allowed) or name ("*.txt"). */
export interface FileFilter {
  /** Shown in the filter menu, e.g. "Text Files". */
  name: string
  mimeTypes?: string[]
  /** Shell-style patterns matched against the name, ignoring case. */
  patterns?: string[]
}

export interface ChooseFileOptions {
  /** Open existing files, name a file to save, or pick a folder. */
  mode: 'open' | 'save' | 'folder'
  /** Window title; defaults to "Open File", "Save File" or "Select Folder". */
  title?: string
  /** Label of the accepting button; defaults to Open, Save or Select. */
  accept?: string
  /** Folder to start in; defaults to the last one used, then the home folder. */
  folder?: string
  /** Save: the suggested file name. */
  name?: string
  /** Open: allow choosing several files. */
  multiple?: boolean
  /** The first one is applied; folders are always listed. Omitted: all files. */
  filters?: FileFilter[]
}

export interface ChosenFiles {
  /** Absolute paths: one, unless multiple was set. */
  paths: string[]
  /** Save: the file exists and the user agreed to replace it. */
  replace: boolean
}

/**
 * The shared file chooser (D49): a modal dialog window of the app that asks
 * (stores/windows.ts openDialog), answered through this store. Closing the
 * dialog, or its parent, answers null.
 */
export const useFileChooser = defineStore('fileChooser', () => {
  const pending = new Map<string, (result: ChosenFiles | null) => void>()
  /** Where the last chooser was answered; the next one starts there. */
  const lastFolder = ref('')

  /** Opens the chooser over parentId; null if cancelled or a dialog is already open. */
  function choose(parentId: string, options: ChooseFileOptions): Promise<ChosenFiles | null> {
    const wm = useWindows()
    const id = wm.openDialog(parentId, 'file-chooser', { options })
    if (!id) return Promise.resolve(null)
    return new Promise((resolve) => {
      pending.set(id, resolve)
      wm.onClose(id, () => settle(id, null))
    })
  }

  function settle(id: string, result: ChosenFiles | null) {
    const resolve = pending.get(id)
    pending.delete(id)
    resolve?.(result)
  }

  /** The chooser's answer; closes it. */
  function answer(id: string, result: ChosenFiles | null, folder?: string) {
    if (folder && result) lastFolder.value = folder
    settle(id, result)
    useWindows().close(id)
  }

  /** Answers every open chooser with null (logout, lock). */
  function reset() {
    for (const id of [...pending.keys()]) settle(id, null)
  }

  return { choose, answer, reset, lastFolder }
})
