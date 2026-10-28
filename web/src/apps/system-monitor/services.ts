// The Services tab's list (D60): search, sorting and the words systemd's
// states are shown with, apart from the component to test them.
import type { Service } from '@/api/types'

export type ServiceSortKey = 'name' | 'state' | 'boot'

/** A service's name as people say it: without ".service". */
export function serviceName(s: Service): string {
  return s.name.replace(/\.service$/, '')
}

/** What the service is doing, in words. */
export function serviceState(s: Service): string {
  switch (s.active) {
    case 'active':
      return s.sub === 'running' ? 'Running' : s.sub === 'exited' ? 'Exited' : 'Active'
    case 'failed':
      return 'Failed'
    case 'activating':
      return 'Starting…'
    case 'deactivating':
      return 'Stopping…'
    case 'reloading':
      return 'Reloading…'
    case 'inactive':
      return 'Inactive'
  }
  return s.active
}

/** Whether it starts at boot, from its unit file. */
export function bootLabel(s: Service): string {
  switch (s.boot) {
    case 'enabled':
    case 'enabled-runtime':
      return 'Enabled'
    case 'disabled':
      return 'Disabled'
    case 'static':
      return 'Static'
    case 'masked':
    case 'masked-runtime':
      return 'Masked'
    case 'indirect':
      return 'Indirect'
    case 'generated':
    case 'transient':
      return 'Generated'
    case 'alias':
      return 'Alias'
    case undefined:
    case '':
      return ''
  }
  return s.boot
}

const running = (s: Service) => s.active === 'active' || s.active === 'activating' || s.active === 'reloading'

export function canStart(s: Service): boolean {
  return !running(s) && s.boot !== 'masked' && s.load !== 'masked'
}

export function canStop(s: Service): boolean {
  return running(s)
}

/** Failed first by state, then running, then the rest. */
function stateRank(s: Service): number {
  if (s.active === 'failed') return 0
  if (running(s)) return 1
  return 2
}

export function visibleServices(list: Service[], query: string, key: ServiceSortKey, desc: boolean): Service[] {
  const q = query.trim().toLowerCase()
  const rows = q
    ? list.filter((s) => s.name.toLowerCase().includes(q) || !!s.description?.toLowerCase().includes(q))
    : [...list]
  const dir = desc ? -1 : 1
  const byName = (a: Service, b: Service) => a.name.localeCompare(b.name)
  return rows.sort((a, b) => {
    const c =
      key === 'state' ? stateRank(a) - stateRank(b) : key === 'boot' ? bootLabel(a).localeCompare(bootLabel(b)) : 0
    return c * dir || byName(a, b) * (key === 'name' ? dir : 1)
  })
}
