// The desktop's keyboard shortcuts (D53): Cinnamon's own keys, which reach
// the page only when the browser lets them through (Keyboard Lock in
// fullscreen, lib/fullscreen.ts), and alternates that any browser passes on,
// with Alt+Shift standing in for Super. Keys are matched by physical key
// (KeyboardEvent.code), so they sit in the same place on every layout.

export type ShortcutAction =
  | 'menu'
  | 'switch'
  | 'switch-back'
  | 'tile-left'
  | 'tile-right'
  | 'tile-up'
  | 'tile-down'
  | 'show-desktop'
  | 'close'
  | 'window-menu'
  | 'move'
  | 'resize'
  | 'maximize'
  | 'terminal'
  | 'lock'

export interface Keys {
  code: string
  ctrl?: true
  alt?: true
  shift?: true
  super?: true
}

export interface Shortcut {
  action: ShortcutAction
  /** Cinnamon's keys. */
  cinnamon: Keys[]
  /** Keys that reach the page outside fullscreen too. */
  alternate?: Keys
}

const altShift = { alt: true, shift: true } as const

export const shortcuts: Shortcut[] = [
  // Super on its own opens the menu too, in fullscreen (shell/keyboard.ts).
  { action: 'menu', cinnamon: [], alternate: { code: 'F1', alt: true } },
  { action: 'switch', cinnamon: [{ code: 'Tab', alt: true }], alternate: { code: 'Backquote', alt: true } },
  {
    action: 'switch-back',
    cinnamon: [{ code: 'Tab', ...altShift }],
    alternate: { code: 'Backquote', ...altShift },
  },
  {
    action: 'tile-left',
    cinnamon: [{ code: 'ArrowLeft', super: true }],
    alternate: { code: 'ArrowLeft', ...altShift },
  },
  {
    action: 'tile-right',
    cinnamon: [{ code: 'ArrowRight', super: true }],
    alternate: { code: 'ArrowRight', ...altShift },
  },
  { action: 'tile-up', cinnamon: [{ code: 'ArrowUp', super: true }], alternate: { code: 'ArrowUp', ...altShift } },
  {
    action: 'tile-down',
    cinnamon: [{ code: 'ArrowDown', super: true }],
    alternate: { code: 'ArrowDown', ...altShift },
  },
  { action: 'show-desktop', cinnamon: [{ code: 'KeyD', super: true }], alternate: { code: 'KeyD', ...altShift } },
  { action: 'close', cinnamon: [{ code: 'F4', alt: true }], alternate: { code: 'F4', ...altShift } },
  { action: 'window-menu', cinnamon: [{ code: 'Space', alt: true }], alternate: { code: 'Space', ...altShift } },
  { action: 'move', cinnamon: [{ code: 'F7', alt: true }] },
  { action: 'resize', cinnamon: [{ code: 'F8', alt: true }] },
  { action: 'maximize', cinnamon: [{ code: 'F10', alt: true }] },
  { action: 'terminal', cinnamon: [{ code: 'KeyT', ctrl: true, alt: true }] },
  { action: 'lock', cinnamon: [{ code: 'KeyL', ctrl: true, alt: true }] },
]

function matches(e: KeyboardEvent, k: Keys): boolean {
  return (
    e.code === k.code &&
    e.ctrlKey === !!k.ctrl &&
    e.altKey === !!k.alt &&
    e.shiftKey === !!k.shift &&
    e.metaKey === !!k.super
  )
}

export interface MatchOptions {
  /**
   * Whether Super combinations count. Off for Apple keyboards, where the
   * Meta key is Command, which apps use (Cmd+arrows move the cursor).
   */
  superKeys: boolean
}

/** The shortcut a key press is, if any. */
export function matchShortcut(e: KeyboardEvent, opts: MatchOptions): ShortcutAction | null {
  if (e.isComposing) return null
  // Windows reports AltGr as Ctrl+Alt: AltGr+L types "ł", AltGr+T "ŧ".
  const altGr = e.getModifierState('AltGraph')
  for (const s of shortcuts) {
    for (const k of s.alternate ? [...s.cinnamon, s.alternate] : s.cinnamon) {
      if (k.super && !opts.superKeys) continue
      if (k.ctrl && k.alt && altGr) continue
      if (matches(e, k)) return s.action
    }
  }
  return null
}

/** Super (Meta, or "OS" in older Firefox) itself. */
export function isSuperKey(e: KeyboardEvent): boolean {
  return e.key === 'Meta' || e.key === 'OS'
}

/** Whether the browser runs on a Mac or an iPad, where Meta is Command. */
export function appleKeyboard(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
}
