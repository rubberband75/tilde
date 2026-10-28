// The whole desktop in fullscreen, with Keyboard Lock where the browser has
// it (Chromium): then Alt+Tab, Super, Alt+F4 and the browser's own keys
// (Ctrl+W, Ctrl+T) come to Tilde instead of the operating system and the
// browser, and holding Escape leaves fullscreen (D53).
import { ref } from 'vue'

interface KeyboardLock {
  lock(codes?: string[]): Promise<void>
  unlock(): void
}

function keyboard(): KeyboardLock | undefined {
  const kb = (navigator as Navigator & { keyboard?: Partial<KeyboardLock> }).keyboard
  return kb?.lock && kb.unlock ? (kb as KeyboardLock) : undefined
}

/** The desktop is fullscreen (not a window's content, as the image viewer's). */
export const desktopFullscreen = ref(false)
/** Every key comes to Tilde: fullscreen with Keyboard Lock. */
export const keyboardLocked = ref(false)

function update() {
  // A window's content going fullscreen on top (the image viewer) leaves
  // the desktop fullscreen underneath.
  if (document.fullscreenElement === document.documentElement) desktopFullscreen.value = true
  else if (!document.fullscreenElement) desktopFullscreen.value = false
  if (!document.fullscreenElement && keyboardLocked.value) {
    keyboard()?.unlock()
    keyboardLocked.value = false
  }
}
document.addEventListener('fullscreenchange', update)

/** Enters or leaves fullscreen; false if the browser refused. */
export async function toggleDesktopFullscreen(): Promise<boolean> {
  try {
    if (desktopFullscreen.value) {
      await document.exitFullscreen()
      return true
    }
    await document.documentElement.requestFullscreen({ navigationUI: 'hide' })
  } catch {
    return false
  }
  const kb = keyboard()
  if (kb) {
    try {
      await kb.lock()
      keyboardLocked.value = true
    } catch {
      // Fullscreen without the lock: the alternates still work.
    }
  }
  return true
}
