import type { Component } from 'vue'
import type { Entry } from '@/api/types'
import type { ClipboardOp } from '@/stores/clipboard'
import type { ConfirmOptions, PromptOptions } from '@/stores/dialogs'
import type { ChooseFileOptions, ChosenFiles, FileFilter } from '@/stores/fileChooser'

export type { ChooseFileOptions, ChosenFiles, ClipboardOp, ConfirmOptions, FileFilter, PromptOptions }

/** The file clipboard shared by Files windows and the desktop. */
export interface FileClipboard {
  readonly op: ClipboardOp | null
  readonly paths: readonly string[]
  set(op: ClipboardOp, paths: string[]): void
  clear(): void
}

/**
 * Everything the shell needs to know about an app (D18). Built-in
 * apps register in ./registry.ts; runtime plugins later use the same shape.
 */
export interface AppManifest {
  id: string
  name: string
  /** One-line description shown in the menu. */
  comment: string
  /** freedesktop icon name from the bundled theme. */
  icon: string
  /** Menu category, as in the Cinnamon menu. */
  category:
    | 'Accessories'
    | 'Graphics'
    | 'Internet'
    | 'Office'
    | 'Sound & Video'
    | 'Administration'
    | 'Preferences'
    | 'System Tools'
  /** MIME types the app opens; "image/*" style wildcards are allowed. */
  mimeTypes?: string[]
  /** Only one window; launching again focuses it. */
  singleInstance?: boolean
  /** Hide from the menu (helpers launched by other apps). */
  hidden?: boolean
  defaultSize: { w: number; h: number }
  minSize?: { w: number; h: number }
  component: () => Promise<Component>
}

/**
 * The shell API every app receives as its `ctx` prop (D18). Apps
 * must not reach into the window store directly.
 */
export interface AppContext {
  readonly windowId: string
  /** Launch arguments plus anything saved with saveState. */
  readonly args: Readonly<Record<string, unknown>>
  setTitle(title: string): void
  setIcon(icon: string): void
  /** Request closing the window (runs the close guard). */
  close(): void
  /** Merge state into args so a reload restores it (D18). */
  saveState(patch: Record<string, unknown>): void
  /** Ask before closing, e.g. for unsaved changes. Return false to cancel. */
  setCloseGuard(guard: (() => boolean | Promise<boolean>) | null): void
  /** Run when the user closes the window (not when the page reloads). */
  onClose(fn: () => void): void
  /** The logged-in user's home directory. */
  readonly home: string
  /** True while this window is focused. Reactive inside watch/computed. */
  isFocused(): boolean
  /**
   * True while the user drags or resizes any window. Apps with expensive
   * layout (the terminal) wait for it to end. Reactive inside watch/computed.
   */
  isInteracting(): boolean
  /** Launch another app. */
  launch(appId: string, args?: Record<string, unknown>): void
  /**
   * The saved state (args) of this app's other windows in this browser:
   * the text editor leaves the drafts they show out of its list to
   * recover (D58). Not other apps' windows.
   */
  appWindows(): ReadonlyArray<Readonly<Record<string, unknown>>>
  /** Open a file or folder with the app registered for its type. */
  openEntry(entry: Entry): void
  notify(message: string, kind?: 'info' | 'error'): void
  /** Ask a question in a modal dialog; resolves true if confirmed. */
  confirm(opts: ConfirmOptions): Promise<boolean>
  /** Ask for a line of text in a modal dialog; resolves null if cancelled. */
  prompt(opts: PromptOptions): Promise<string | null>
  /**
   * Asks for files to open, a file name to save as, or a folder, in the
   * shared file chooser, a modal dialog of this window (D49). Resolves
   * null if cancelled.
   */
  chooseFile(opts: ChooseFileOptions): Promise<ChosenFiles | null>
  /** The shared file clipboard (cut, copy, paste). Reactive. */
  readonly clipboard: FileClipboard
}
