// A tab dragged between editor windows (D63). Every window runs in the
// same page, so the tab itself travels here rather than in the drag's
// data, which a drop target cannot read while the drag is over it.
import type { TabSpec } from './tabs'

/** The drag's own type: windows and the desktop tell tab drags apart by it. */
export const TAB_DRAG = 'application/x-tilde-editor-tab'

interface Move {
  /** The window the tab comes from. */
  from: string
  tab: TabSpec
  /** The tab's unsaved changes written to its draft, before another window opens it. */
  written: Promise<void>
  /** Another window took it: its first window lets it go, keeping the draft. */
  taken: boolean
}

let move: Move | null = null

export function startMove(from: string, tab: TabSpec, written: Promise<void>): void {
  move = { from, tab, written: written.catch(() => {}), taken: false }
}

/** The tab being dragged, if it comes from another window than `here`. */
export function moveFromElsewhere(here: string): Move | null {
  return move && move.from !== here ? move : null
}

/**
 * Takes the tab being dragged into window `here`: resolves with it once its
 * draft is written, or null if there is none to take.
 */
export async function takeMove(here: string): Promise<TabSpec | null> {
  const m = moveFromElsewhere(here)
  if (!m) return null
  m.taken = true
  await m.written
  return m.tab
}

/** Ends the drag in its first window: whether another window took the tab. */
export function endMove(): boolean {
  const taken = !!move?.taken
  move = null
  return taken
}
