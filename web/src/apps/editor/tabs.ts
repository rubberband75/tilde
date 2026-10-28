// The text editor's tabs (D63), as xed's: each tab is one document, with
// its own draft (D48). A window's state keeps the list and the active tab;
// windows from before tabs kept one document, and come back as one tab.

export interface TabSpec {
  /** Names the tab in this window; a new id remounts its editor. */
  id: string
  /** The file, once it has one. */
  path?: string
  /** Names the draft of a document never saved. */
  draft: string
  wrap?: boolean
}

export interface Tabs {
  tabs: TabSpec[]
  active: number
}

const newId = () => crypto.randomUUID()

/** A new, empty document. */
export function newTab(): TabSpec {
  return { id: newId(), draft: newId() }
}

function specFrom(v: unknown): TabSpec | null {
  if (!v || typeof v !== 'object') return null
  const o = v as Record<string, unknown>
  const draft = typeof o.draft === 'string' && o.draft ? o.draft : newId()
  const spec: TabSpec = { id: typeof o.id === 'string' && o.id ? o.id : newId(), draft }
  if (typeof o.path === 'string' && o.path) spec.path = o.path
  if (o.wrap === true) spec.wrap = true
  return spec
}

/** The tabs a window's saved state names, at least one. */
export function tabsFromArgs(args: Readonly<Record<string, unknown>>): Tabs {
  let tabs: TabSpec[] = []
  if (Array.isArray(args.tabs)) tabs = args.tabs.map(specFrom).filter((t): t is TabSpec => !!t)
  else if (typeof args.path === 'string' || typeof args.draft === 'string') {
    // A window from before tabs, or one opened for a single document.
    const one = specFrom(args)
    if (one) tabs = [one]
  }
  if (tabs.length === 0) tabs = [newTab()]
  const active = typeof args.active === 'number' ? args.active : tabs.length - 1
  return { tabs, active: Math.min(Math.max(0, Math.trunc(active)), tabs.length - 1) }
}

/** The draft key a tab's document uses (document.ts). */
export function tabKey(t: Pick<TabSpec, 'path' | 'draft'>): string {
  return t.path ? `file:${t.path}` : `new:${t.draft}`
}

/**
 * Where a file goes: its tab if it is open already; the active tab if
 * that is an empty document nobody touched (it gives its place, as in
 * xed); otherwise a new tab after the active one.
 */
export function openPath(state: Tabs, path: string, untouched: (t: TabSpec) => boolean): Tabs {
  const open = state.tabs.findIndex((t) => t.path === path)
  if (open >= 0) return { tabs: state.tabs, active: open }
  const spec: TabSpec = { id: newId(), path, draft: newId() }
  const current = state.tabs[state.active]
  if (current && untouched(current)) {
    const tabs = [...state.tabs]
    tabs[state.active] = spec
    return { tabs, active: state.active }
  }
  const tabs = [...state.tabs]
  tabs.splice(state.active + 1, 0, spec)
  return { tabs, active: state.active + 1 }
}

/** The tab after closing the active one: the next, or the one before. */
export function afterClose(state: Tabs, id: string): Tabs {
  const i = state.tabs.findIndex((t) => t.id === id)
  if (i < 0) return state
  const tabs = state.tabs.filter((t) => t.id !== id)
  let active = state.active
  if (i < active || active >= tabs.length) active = Math.max(0, active - 1)
  return { tabs, active }
}

/** Moves the tab at from to before the tab at to. */
export function moveTab(state: Tabs, from: number, to: number): Tabs {
  if (from === to || from < 0 || from >= state.tabs.length) return state
  const tabs = [...state.tabs]
  const [t] = tabs.splice(from, 1)
  const at = Math.min(Math.max(0, to > from ? to - 1 : to), tabs.length)
  tabs.splice(at, 0, t!)
  const activeId = state.tabs[state.active]?.id
  return {
    tabs,
    active: Math.max(
      0,
      tabs.findIndex((x) => x.id === activeId),
    ),
  }
}
