import type { Component } from 'vue'
import type { Entry } from '@/api/types'

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
  category: 'Accessories' | 'Graphics' | 'Internet' | 'Office' | 'Sound & Video' | 'Administration' | 'Preferences' | 'System Tools'
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
  /** Open a file or folder with the app registered for its type. */
  openEntry(entry: Entry): void
  notify(message: string, kind?: 'info' | 'error'): void
}
