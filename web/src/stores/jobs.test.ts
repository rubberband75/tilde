import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Job } from '@/api/types'
import { onFsChanged } from '@/lib/bus'
import { affectedDirs, useJobs } from './jobs'

function job(over: Partial<Job>): Job {
  return {
    id: 'j1',
    op: 'copy',
    sources: ['/home/me/a.txt', '/home/me/b.txt'],
    dest: '/home/me/Documents',
    state: 'running',
    totalItems: 2,
    doneItems: 0,
    totalBytes: 10,
    doneBytes: 0,
    errors: [],
    errorCount: 0,
    started: '2026-10-20T12:00:00Z',
    ...over,
  }
}

beforeEach(() => setActivePinia(createPinia()))

describe('jobs store', () => {
  it('applies snapshots, updates and removals', () => {
    const jobs = useJobs()
    jobs.onEvent({ type: 'snapshot', jobs: [job({}), job({ id: 'j2', started: '2026-10-20T11:00:00Z' })] })
    expect(jobs.jobs.map((j) => j.id)).toEqual(['j2', 'j1'])
    jobs.onEvent({
      type: 'job',
      job: job({ state: 'waiting', conflict: { id: 1 } as Job['conflict'] }),
    })
    expect(jobs.conflicted?.id).toBe('j1')
    jobs.onEvent({ type: 'job-removed', id: 'j1' })
    expect(jobs.jobs.map((j) => j.id)).toEqual(['j2'])
    expect(jobs.conflicted).toBeUndefined()
  })

  it('refreshes the touched folders once when a job finishes', () => {
    const changed = vi.fn()
    const off = onFsChanged(changed)
    const jobs = useJobs()
    jobs.onEvent({ type: 'job', job: job({}) })
    expect(changed).not.toHaveBeenCalled()
    const done = job({ state: 'done', finished: '2026-10-20T12:00:01Z' })
    jobs.onEvent({ type: 'job', job: done })
    jobs.onEvent({ type: 'job', job: done })
    expect(changed.mock.calls.map((c) => c[0])).toEqual(['/home/me', '/home/me/Documents'])
    off()
  })
})

describe('fs events', () => {
  it('pass changed folders on to the windows', () => {
    const changed = vi.fn()
    const off = onFsChanged(changed)
    useJobs().onEvent({ type: 'fs', dirs: ['/home/me', '/home/me/Desktop'] })
    expect(changed.mock.calls.map((c) => c[0])).toEqual(['/home/me', '/home/me/Desktop'])
    off()
  })
})

describe('affectedDirs', () => {
  it('lists source folders and the destination once', () => {
    expect(affectedDirs(job({ op: 'delete', dest: undefined, sources: ['/a/x', '/a/y', '/b/z'] }))).toEqual([
      '/a',
      '/b',
    ])
  })
})
