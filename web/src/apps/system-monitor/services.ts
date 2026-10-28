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

/** Only services with an [Install] section can be switched on or off at boot. */
export function canSwitchBoot(s: Service): boolean {
  return s.boot === 'enabled' || s.boot === 'disabled'
}

/** Why the boot switch is off, for the services it cannot change. */
export function bootNote(s: Service): string {
  switch (s.boot) {
    case 'static':
      return 'Started by other services or on demand, not at boot by itself.'
    case 'masked':
    case 'masked-runtime':
      return 'Masked: it cannot be started at all.'
    case 'generated':
    case 'transient':
      return 'Made by systemd at boot, from other configuration.'
    case 'indirect':
      return 'Started through another unit.'
    case 'alias':
      return 'Another name for a service.'
    case undefined:
    case '':
      return 'It has no unit file.'
  }
  return ''
}

/** A journal line's time: the time of day, with the date when not today. */
export function journalTime(ms: number, now: Date = new Date()): string {
  const d = new Date(ms)
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  if (d.toDateString() === now.toDateString()) return time
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}`
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
