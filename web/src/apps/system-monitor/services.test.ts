import { describe, expect, it } from 'vitest'
import type { Service } from '@/api/types'
import { bootLabel, canStart, canStop, serviceName, serviceState, visibleServices } from './services'

const svc = (name: string, active: string, sub: string, boot?: string, description = ''): Service => ({
  name,
  active,
  sub,
  boot,
  description,
  load: 'loaded',
})

const list = [
  svc('ssh.service', 'active', 'running', 'enabled', 'OpenBSD Secure Shell server'),
  svc('cups.service', 'inactive', 'dead', 'disabled', 'CUPS Scheduler'),
  svc('broken.service', 'failed', 'failed', 'static', 'Breaks'),
  svc('apparmor.service', 'active', 'exited', 'enabled', 'Load AppArmor profiles'),
]

describe('services', () => {
  it('say what a service does, and whether it starts at boot', () => {
    expect(list.map(serviceState)).toEqual(['Running', 'Inactive', 'Failed', 'Exited'])
    expect(serviceState(svc('x.service', 'activating', 'start'))).toBe('Starting…')
    expect(list.map(bootLabel)).toEqual(['Enabled', 'Disabled', 'Static', 'Enabled'])
    expect(bootLabel(svc('x.service', 'inactive', 'dead'))).toBe('')
    expect(serviceName(list[0]!)).toBe('ssh')
  })

  it('offer Start or Stop as the state allows', () => {
    expect(list.map(canStart)).toEqual([false, true, true, false])
    expect(list.map(canStop)).toEqual([true, false, false, true])
    expect(canStart(svc('m.service', 'inactive', 'dead', 'masked'))).toBe(false)
  })

  it('search names and descriptions, and sort', () => {
    const names = (rows: Service[]) => rows.map(serviceName)
    expect(names(visibleServices(list, 'shell', 'name', false))).toEqual(['ssh'])
    expect(names(visibleServices(list, '', 'name', false))).toEqual(['apparmor', 'broken', 'cups', 'ssh'])
    expect(names(visibleServices(list, '', 'state', false))).toEqual(['broken', 'apparmor', 'ssh', 'cups'])
    expect(names(visibleServices(list, '', 'name', true))).toEqual(['ssh', 'cups', 'broken', 'apparmor'])
  })
})
