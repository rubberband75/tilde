import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { connectEvents, type EventStream } from '@/api/events'
import { jobsApi } from '@/api/jobs'
import type { Job, WorkerEvent } from '@/api/types'
import { dirName } from '@/api/fs'
import { emitFsChanged } from '@/lib/bus'

/** Folders whose listing a job changes. */
export function affectedDirs(job: Job): string[] {
  const dirs = job.sources.map(dirName)
  if (job.dest) dirs.push(job.dest)
  return [...new Set(dirs)]
}

/**
 * File operation jobs of this user, kept in sync with the worker over the
 * events WebSocket. Jobs started on another device show up here too.
 */
export const useJobs = defineStore('jobs', () => {
  const byId = ref<Record<string, Job>>({})
  let stream: EventStream | undefined

  const jobs = computed(() => Object.values(byId.value).sort((a, b) => a.started.localeCompare(b.started)))
  /** The job whose conflict the user is asked about; one at a time. */
  const conflicted = computed(() => jobs.value.find((j) => j.state === 'waiting' && j.conflict))

  function apply(job: Job) {
    const before = byId.value[job.id]
    byId.value = { ...byId.value, [job.id]: job }
    // Refresh the folders a job touched once it ends. Phase 4 replaces this
    // with inotify events.
    if (job.finished && !before?.finished) emitFsChanged(...affectedDirs(job))
  }

  function onEvent(ev: WorkerEvent) {
    switch (ev.type) {
      case 'snapshot':
        byId.value = Object.fromEntries(ev.jobs.map((j) => [j.id, j]))
        break
      case 'job':
        apply(ev.job)
        break
      case 'job-removed': {
        const next = { ...byId.value }
        delete next[ev.id]
        byId.value = next
        break
      }
    }
  }

  function connect() {
    stream?.close()
    stream = connectEvents(onEvent)
  }

  function disconnect() {
    stream?.close()
    stream = undefined
    byId.value = {}
  }

  /** Starts a job; progress arrives over the events socket. */
  async function start(...args: Parameters<typeof jobsApi.start>) {
    const job = await jobsApi.start(...args)
    if (!byId.value[job.id]) apply(job)
    return job
  }

  return { jobs, conflicted, connect, disconnect, start, onEvent }
})
