import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { connectEvents, type EventStream } from '@/api/events'
import { jobsApi } from '@/api/jobs'
import type { Job, WorkerEvent } from '@/api/types'
import { dirName } from '@/api/fs'
import { emitDrivesChanged, emitFsChanged } from '@/lib/bus'
import { setWatchSender, watchedDirs } from '@/lib/fswatch'

/** Folders whose listing a job changes. */
export function affectedDirs(job: Job): string[] {
  const dirs = job.sources.map(dirName)
  if (job.dest) dirs.push(job.dest)
  return [...new Set(dirs)]
}

/**
 * File operation jobs of this user, kept in sync with the worker over the
 * events WebSocket. Jobs started on another device show up here too. The
 * store also owns that socket's other traffic: the watched folders
 * (lib/fswatch.ts) go up, and changes to them come back as emitFsChanged.
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
    // Refresh the folders a job touched once it ends. The worker reports
    // watched folders by itself; this also covers search results below them
    // and works while the socket is down.
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
      case 'fs':
        emitFsChanged(...ev.dirs)
        break
      case 'drives':
        emitDrivesChanged()
        break
    }
  }

  function connect() {
    stream?.close()
    const s = connectEvents(onEvent, (reconnected) => {
      s.send({ type: 'watch', dirs: watchedDirs() })
      // Changes made while the socket was down were not reported.
      if (reconnected) emitFsChanged(...watchedDirs())
    })
    stream = s
    setWatchSender((dirs) => s.send({ type: 'watch', dirs }))
  }

  function disconnect() {
    setWatchSender(undefined)
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
