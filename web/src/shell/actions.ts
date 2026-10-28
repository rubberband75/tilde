import { appForMime } from '@/apps/registry'
import type { AppContext } from '@/apps/types'
import { fsApi } from '@/api/fs'
import type { Entry } from '@/api/types'
import { saveBeforeLock } from '@/lib/beforeLock'
import { useClipboard } from '@/stores/clipboard'
import { useDialogs } from '@/stores/dialogs'
import { useFileChooser } from '@/stores/fileChooser'
import { useNotices } from '@/stores/notices'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'

/** Opens a folder in Files, or a file with the app registered for its type. */
export function openEntry(entry: Entry): void {
  const wm = useWindows()
  if (entry.type === 'dir') {
    wm.open('files', { path: entry.path })
    return
  }
  const app = appForMime(entry.mime)
  if (app) {
    wm.open(app.id, { path: entry.path })
    return
  }
  // No viewer app yet: let the browser display it in a new tab. The worker
  // serves files with a sandbox CSP, so nothing in them can run as Tilde.
  window.open(fsApi.contentUrl(entry.path), '_blank', 'noopener')
}

/** Locks every browser logged in as this user (menu button, Ctrl+Alt+L). */
export async function lockScreen(): Promise<void> {
  await saveBeforeLock()
  try {
    await useSession().lock()
  } catch {
    useNotices().push('Could not lock the screen.', 'error')
  }
}

/** Builds the shell API handed to an app window (see apps/types.ts). */
export function createContext(windowId: string): AppContext {
  const wm = useWindows()
  const notices = useNotices()
  const session = useSession()
  const dialogs = useDialogs()
  const clipboard = useClipboard()
  const chooser = useFileChooser()
  return {
    windowId,
    get args() {
      return wm.get(windowId)?.args ?? {}
    },
    setTitle: (title) => wm.setTitle(windowId, title),
    setIcon: (icon) => wm.setIcon(windowId, icon),
    close: () => void wm.requestClose(windowId),
    saveState: (patch) => wm.saveState(windowId, patch),
    onLaunch: (handler) => wm.setLaunchHandler(windowId, handler),
    appWindows: () => {
      const appId = wm.get(windowId)?.appId
      return wm.windows.filter((w) => w.appId === appId && w.id !== windowId).map((w) => w.args)
    },
    setCloseGuard: (guard) => wm.setCloseGuard(windowId, guard),
    onClose: (fn) => wm.onClose(windowId, fn),
    get home() {
      return session.info?.home ?? '/'
    },
    isFocused: () => wm.focusedId === windowId,
    isInteracting: () => wm.interacting,
    launch: (appId, args) => void wm.open(appId, args),
    openEntry,
    notify: (message, kind) => notices.push(message, kind),
    confirm: (opts) => dialogs.confirm(opts),
    prompt: (opts) => dialogs.prompt(opts),
    chooseFile: (opts) => chooser.choose(windowId, opts),
    clipboard: {
      get op() {
        return clipboard.op
      },
      get paths() {
        return clipboard.paths
      },
      set: (op, paths) => clipboard.set(op, paths),
      clear: () => clipboard.clear(),
    },
  }
}
