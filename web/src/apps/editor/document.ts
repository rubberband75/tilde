// One document of the text editor (D48): the file it is, the text as last
// saved, and the unsaved changes, which go to the worker as a draft while
// the user types. Locking clears the page (D40) and browsers reload, so a
// window that comes back finds its changes in the draft. This holds no
// editor: EditorApp.vue passes the text in and shows what comes back.
import { reactive } from 'vue'
import { ApiError } from '@/api/client'
import { baseName } from '@/api/fs'
import { draftsApi, textApi } from '@/api/text'

/** How long after the last change the draft is written. */
export const draftDelayMs = 500

export interface DocState {
  /** The file, or '' for a document not saved yet. */
  path: string
  /** The file's version the saved text is. */
  version: string
  /** The user may save to the file. */
  writable: boolean
  /** The text differs from the file. */
  dirty: boolean
  /** The file changed on disk after the unsaved changes began. */
  changedOnDisk: boolean
  /** The file is gone (deleted or moved) while it has unsaved changes. */
  missing: boolean
}

/** What a save came to: done, or a question for the user first. */
export type SaveResult = 'saved' | 'changed' | 'exists' | 'needs-path'

export class EditorDocument {
  readonly state: DocState
  private saved = ''
  private current = ''
  /** Reads the editor's text, after a change not drafted yet. */
  private source: (() => string) | undefined
  private timer: ReturnType<typeof setTimeout> | undefined
  /** A draft may exist in the worker under the current key. */
  private drafted = false

  /** draftId names the draft of a document not saved yet. */
  constructor(
    path: string,
    private readonly draftId: string,
  ) {
    this.state = reactive({ path, version: '', writable: true, dirty: false, changedOnDisk: false, missing: false })
  }

  get name(): string {
    return this.state.path ? baseName(this.state.path) : 'Untitled Document'
  }

  get key(): string {
    return this.state.path ? `file:${this.state.path}` : `new:${this.draftId}`
  }

  /** Reads the file and any draft; returns the text to show. */
  async open(): Promise<string> {
    let file = null
    if (this.state.path) {
      try {
        file = await textApi.get(this.state.path)
      } catch (e) {
        if (!(e instanceof ApiError && e.code === 'not_found')) throw e
      }
    }
    const draft = await draftsApi.get(this.key).catch((e: unknown) => {
      if (e instanceof ApiError && e.code === 'not_found') return null
      throw e
    })
    if (this.state.path && !file && !draft) throw new ApiError(404, 'not_found', `“${this.name}” does not exist.`)
    this.drafted = !!draft
    if (file) {
      this.saved = file.text
      this.state.version = file.version
      this.state.writable = file.writable
    } else {
      this.state.missing = !!this.state.path
    }
    this.current = draft?.text ?? this.saved
    this.state.dirty = this.current !== this.saved
    this.state.changedOnDisk = !!file && !!draft && this.state.dirty && draft.base !== file.version
    if (draft && !this.state.dirty) void this.dropDraft()
    return this.current
  }

  /**
   * The user changed the text. It is read (through text, for a large
   * document only once) and drafted after a short pause; until then the
   * document counts as modified.
   */
  edited(text: string | (() => string)): void {
    this.source = typeof text === 'string' ? () => text : text
    this.state.dirty = true
    clearTimeout(this.timer)
    this.timer = setTimeout(() => void this.flush(), draftDelayMs)
  }

  private text(): string {
    if (this.source) {
      this.current = this.source()
      this.source = undefined
    }
    return this.current
  }

  /** Writes the pending draft now (before locking or closing the page). */
  async flush(): Promise<void> {
    clearTimeout(this.timer)
    this.timer = undefined
    this.state.dirty = this.text() !== this.saved
    if (this.state.dirty) {
      await draftsApi.put(this.key, this.current, this.state.version)
      this.drafted = true
    } else if (this.drafted) {
      await this.dropDraft()
    }
  }

  /** Saves to the file; 'changed' and 'exists' need force to go ahead. */
  async save(text: string, force = false): Promise<SaveResult> {
    if (!this.state.path) return 'needs-path'
    return this.write(this.state.path, text, this.state.version, force)
  }

  /** Saves as another file, which then is this document's. */
  async saveAs(path: string, text: string, force = false): Promise<SaveResult> {
    const oldKey = this.key
    const result = await this.write(path, text, '', force)
    if (result === 'saved' && oldKey !== this.key) await draftsApi.remove(oldKey).catch(() => {})
    return result
  }

  private async write(path: string, text: string, base: string, force: boolean): Promise<SaveResult> {
    let res
    try {
      res = await textApi.save(path, text, base, force)
    } catch (e) {
      if (e instanceof ApiError && (e.code === 'changed' || e.code === 'exists')) return e.code
      throw e
    }
    this.state.path = path
    this.state.version = res.version
    this.state.writable = true
    this.state.missing = false
    this.state.changedOnDisk = false
    this.saved = text
    this.state.dirty = this.text() !== text
    if (!this.state.dirty) {
      clearTimeout(this.timer)
      await this.dropDraft()
    }
    return 'saved'
  }

  /** Reads the file again, dropping the unsaved changes. */
  async reload(): Promise<string> {
    const file = await textApi.get(this.state.path)
    this.saved = this.current = file.text
    this.source = undefined
    this.state.version = file.version
    this.state.writable = file.writable
    this.state.dirty = this.state.changedOnDisk = this.state.missing = false
    clearTimeout(this.timer)
    await this.dropDraft()
    return file.text
  }

  /**
   * The file's folder changed. Without unsaved changes, a new version on
   * disk is read and returned; with them, changedOnDisk is set instead.
   */
  async checkDisk(): Promise<string | null> {
    if (!this.state.path) return null
    let file
    try {
      file = await textApi.get(this.state.path)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') this.state.missing = true
      return null
    }
    this.state.missing = false
    if (file.version === this.state.version) return null
    if (this.state.dirty) {
      this.state.changedOnDisk = true
      return null
    }
    this.saved = this.current = file.text
    this.state.version = file.version
    this.state.writable = file.writable
    return file.text
  }

  /** Forgets the unsaved changes (closing without saving). */
  async discard(): Promise<void> {
    clearTimeout(this.timer)
    this.source = undefined
    this.current = this.saved
    this.state.dirty = false
    await this.dropDraft()
  }

  private async dropDraft() {
    this.drafted = false
    await draftsApi.remove(this.key).catch(() => {})
  }
}
