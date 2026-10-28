// Runs the desktop's keyboard shortcuts (shortcuts.ts, D53). The handlers
// listen on the window in the capture phase, before any app: a shortcut is
// the shell's, as in Cinnamon, and the app never sees it.
import { keyboardLocked } from '@/lib/fullscreen'
import { useShell } from '@/stores/shell'
import { useSwitcher } from '@/stores/switcher'
import { useWindows } from '@/stores/windows'
import type { Direction } from '@/wm/snap'
import { lockScreen } from './actions'
import { appleKeyboard, isSuperKey, matchShortcut, type ShortcutAction } from './shortcuts'

const tileDirections: Partial<Record<ShortcutAction, Direction>> = {
  'tile-left': 'left',
  'tile-right': 'right',
  'tile-up': 'up',
  'tile-down': 'down',
}

/** Installs the shortcuts; returns the function that removes them. */
export function installShortcuts(): () => void {
  const wm = useWindows()
  const shell = useShell()
  const switcher = useSwitcher()
  const opts = { superKeys: !appleKeyboard() }
  /** Super went down and nothing else since: its release opens the menu. */
  let superAlone = false

  const consume = (e: Event) => {
    e.preventDefault()
    e.stopImmediatePropagation()
  }

  function run(action: ShortcutAction) {
    const id = wm.focusedId
    const dir = tileDirections[action]
    if (dir) {
      if (id) wm.pushTile(id, dir)
      return
    }
    switch (action) {
      case 'menu':
        shell.menuOpen = !shell.menuOpen
        break
      case 'switch':
      case 'switch-back':
        switcher.start(action === 'switch-back')
        break
      case 'show-desktop':
        wm.toggleShowDesktop()
        break
      case 'close':
        if (id) void wm.requestClose(id)
        break
      case 'window-menu':
        if (id) wm.requestFrame(id, 'menu')
        break
      case 'move':
      case 'resize':
        if (id) wm.requestFrame(id, action)
        break
      case 'maximize':
        if (id) wm.toggleMaximize(id)
        break
      case 'terminal':
        wm.open('terminal')
        break
      case 'lock':
        void lockScreen()
        break
    }
  }

  function onKeyDown(e: KeyboardEvent) {
    if (switcher.active) {
      // While the switcher is up, every key is its own.
      consume(e)
      if (e.code === 'Tab' || e.code === 'Backquote') switcher.step(e.shiftKey)
      else if (e.code === 'ArrowRight') switcher.step(false)
      else if (e.code === 'ArrowLeft') switcher.step(true)
      else if (e.code === 'Enter') switcher.commit()
      else if (e.code === 'Escape') switcher.cancel()
      return
    }
    if (isSuperKey(e)) {
      if (!e.repeat) superAlone = true
      return
    }
    superAlone = false
    const action = matchShortcut(e, opts)
    if (!action) return
    consume(e)
    run(action)
  }

  function onKeyUp(e: KeyboardEvent) {
    if (switcher.active) {
      consume(e)
      if (!e.altKey) switcher.commit()
      return
    }
    // Only with every key coming here: otherwise the system menu of the
    // machine the browser runs on opens as well.
    if (isSuperKey(e) && superAlone && opts.superKeys && keyboardLocked.value) {
      consume(e)
      shell.menuOpen = !shell.menuOpen
    }
    superAlone = false
  }

  function onBlur() {
    switcher.cancel()
    superAlone = false
  }
  const onPointerDown = () => {
    superAlone = false
  }

  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  window.addEventListener('blur', onBlur)
  window.addEventListener('pointerdown', onPointerDown, true)
  return () => {
    window.removeEventListener('keydown', onKeyDown, true)
    window.removeEventListener('keyup', onKeyUp, true)
    window.removeEventListener('blur', onBlur)
    window.removeEventListener('pointerdown', onPointerDown, true)
    switcher.cancel()
  }
}
