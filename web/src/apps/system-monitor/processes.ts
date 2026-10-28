// The System Monitor's process list (D59): GNOME System Monitor's views,
// search, sorting and labels, kept apart from the component to test them.
import type { Process } from '@/api/types'

export type ProcessView = 'mine' | 'all' | 'active'
export type SortKey = 'name' | 'user' | 'cpu' | 'pid' | 'memory' | 'nice'

export interface Sort {
  key: SortKey
  desc: boolean
}

/** Active: running, or using some CPU, as GNOME counts it. */
function active(p: Process) {
  return p.state === 'R' || p.cpu > 0
}

export function visibleProcesses(list: Process[], view: ProcessView, uid: number, query: string, sort: Sort) {
  const q = query.trim().toLowerCase()
  const rows = list.filter((p) => {
    if (view === 'mine' && p.uid !== uid) return false
    if (view === 'active' && !active(p)) return false
    if (!q) return true
    return (
      p.name.toLowerCase().includes(q) ||
      String(p.pid) === q ||
      p.user.toLowerCase().includes(q) ||
      !!p.command?.toLowerCase().includes(q)
    )
  })
  const dir = sort.desc ? -1 : 1
  const cmp = (a: Process, b: Process): number => {
    switch (sort.key) {
      case 'name':
        return a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true })
      case 'user':
        return a.user.localeCompare(b.user)
      case 'cpu':
        return a.cpu - b.cpu
      case 'memory':
        return a.memory - b.memory
      case 'nice':
        return a.nice - b.nice
      default:
        return a.pid - b.pid
    }
  }
  // Ties by PID, so rows do not swap places between two lists.
  return rows.sort((a, b) => cmp(a, b) * dir || a.pid - b.pid)
}

/** GNOME's names for nice values. */
export function priorityLabel(nice: number): string {
  if (nice < -7) return 'Very High'
  if (nice < -2) return 'High'
  if (nice < 3) return 'Normal'
  if (nice < 7) return 'Low'
  return 'Very Low'
}

export function stateLabel(state: string): string {
  switch (state) {
    case 'R':
      return 'Running'
    case 'S':
    case 'I':
      return 'Sleeping'
    case 'D':
      return 'Uninterruptible'
    case 'Z':
      return 'Zombie'
    case 'T':
    case 't':
      return 'Stopped'
  }
  return state
}

/** The sort a header click gives: numbers start high, names start at A. */
export function nextSort(current: Sort, key: SortKey): Sort {
  if (current.key === key) return { key, desc: !current.desc }
  return { key, desc: key === 'cpu' || key === 'memory' }
}
