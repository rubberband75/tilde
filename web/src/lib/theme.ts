// The look (D77): Mint-Y's modes and accents, applied as attributes on
// <html> that select the token blocks in styles/themes/mint-y.css:
// data-shell (the panel, menus, popups), data-apps (windows), data-accent.
import type { Appearance, DesktopTheme, ThemeAccent, ThemeMode } from '@/api/types'

export const themeModes: ThemeMode[] = ['dark', 'mixed', 'light']
export const themeAccents: ThemeAccent[] = [
  'green',
  'aqua',
  'blue',
  'grey',
  'orange',
  'pink',
  'purple',
  'red',
  'sand',
  'teal',
]

export interface Look {
  mode: ThemeMode
  accent: ThemeAccent
}

/** Tilde's look without settings or a desktop: Mint-Y Dark, green. */
export const defaultLook: Look = { mode: 'dark', accent: 'green' }

/** The look to show: the user's choice, or the desktop's where they follow it. */
export function resolveLook(a: Appearance, desktop: DesktopTheme | null): Look {
  const d = desktop ?? defaultLook
  return {
    mode: a.mode === 'desktop' ? d.mode : a.mode,
    accent: a.accent === 'desktop' ? d.accent : a.accent,
  }
}

/** Mixed is Mint's light windows with a dark panel and menus. */
export function themeAttributes(look: Look): { shell: 'dark' | 'light'; apps: 'dark' | 'light'; accent: ThemeAccent } {
  return {
    shell: look.mode === 'light' ? 'light' : 'dark',
    apps: look.mode === 'dark' ? 'dark' : 'light',
    accent: look.accent,
  }
}

const storageKey = 'tilde:look'

/**
 * Applies a look to the page, and remembers it in this browser so the next
 * visit's login screen shows it before anyone logs in (it says nothing
 * about the user beyond their colours).
 */
export function applyLook(look: Look, el: HTMLElement = document.documentElement) {
  const a = themeAttributes(look)
  el.dataset.shell = a.shell
  el.dataset.apps = a.apps
  el.dataset.accent = a.accent
  try {
    localStorage.setItem(storageKey, JSON.stringify(look))
  } catch {
    // Private windows may refuse; the default look then shows first.
  }
}

/** The look this browser last showed, for the first paint. */
export function rememberedLook(): Look {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey) ?? 'null') as Partial<Look> | null
    if (v && themeModes.includes(v.mode as ThemeMode) && themeAccents.includes(v.accent as ThemeAccent)) {
      return v as Look
    }
  } catch {
    // Unreadable: the default.
  }
  return defaultLook
}
