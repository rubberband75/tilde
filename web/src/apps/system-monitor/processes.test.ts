import { describe, expect, it } from 'vitest'
import type { Process } from '@/api/types'
import { nextSort, priorityLabel, stateLabel, visibleProcesses } from './processes'

const proc = (pid: number, name: string, over: Partial<Process> = {}): Process => ({
  pid,
  ppid: 1,
  name,
  uid: 1000,
  user: 'me',
  state: 'S',
  cpu: 0,
  memory: 1000,
  nice: 0,
  threads: 1,
  start: pid * 10,
  ...over,
})

const list = [
  proc(1, 'systemd', { uid: 0, user: 'root', memory: 9000 }),
  proc(500, 'bash', { command: 'bash --login' }),
  proc(501, 'firefox', { cpu: 12.5, state: 'R', memory: 500_000 }),
  proc(600, 'Xorg', { uid: 0, user: 'root', cpu: 3 }),
  proc(700, 'node', { command: 'node server.js', cpu: 3 }),
]
const pids = (rows: Process[]) => rows.map((p) => p.pid)

describe('visibleProcesses', () => {
  it('shows mine, all or active ones', () => {
    const byPid = { key: 'pid', desc: false } as const
    expect(pids(visibleProcesses(list, 'all', 1000, '', byPid))).toEqual([1, 500, 501, 600, 700])
    expect(pids(visibleProcesses(list, 'mine', 1000, '', byPid))).toEqual([500, 501, 700])
    expect(pids(visibleProcesses(list, 'active', 1000, '', byPid))).toEqual([501, 600, 700])
  })

  it('searches names, users, PIDs and command lines', () => {
    const byPid = { key: 'pid', desc: false } as const
    expect(pids(visibleProcesses(list, 'all', 1000, 'ROOT', byPid))).toEqual([1, 600])
    expect(pids(visibleProcesses(list, 'all', 1000, 'server.js', byPid))).toEqual([700])
    expect(pids(visibleProcesses(list, 'all', 1000, '500', byPid))).toEqual([500])
  })

  it('sorts, ties by PID', () => {
    expect(pids(visibleProcesses(list, 'all', 1000, '', { key: 'cpu', desc: true }))).toEqual([501, 600, 700, 1, 500])
    expect(pids(visibleProcesses(list, 'all', 1000, '', { key: 'name', desc: false }))).toEqual([500, 501, 700, 1, 600])
    expect(pids(visibleProcesses(list, 'all', 1000, '', { key: 'memory', desc: true }))[0]).toBe(501)
  })
})

describe('labels', () => {
  it('name priorities and states as GNOME does', () => {
    expect([-20, -5, 0, 5, 19].map(priorityLabel)).toEqual(['Very High', 'High', 'Normal', 'Low', 'Very Low'])
    expect(['R', 'S', 'D', 'Z', 'T', 'X'].map(stateLabel)).toEqual([
      'Running',
      'Sleeping',
      'Uninterruptible',
      'Zombie',
      'Stopped',
      'X',
    ])
  })

  it('turn a header click into a sort', () => {
    expect(nextSort({ key: 'name', desc: false }, 'name')).toEqual({ key: 'name', desc: true })
    expect(nextSort({ key: 'name', desc: false }, 'cpu')).toEqual({ key: 'cpu', desc: true })
    expect(nextSort({ key: 'cpu', desc: true }, 'user')).toEqual({ key: 'user', desc: false })
  })
})
