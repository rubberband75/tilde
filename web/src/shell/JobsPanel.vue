<script setup lang="ts">
// Progress of file operation jobs (Nemo's "File Operations" window), at the
// bottom right. Quick jobs never show; a card appears after a moment, stays
// while the job waits for an answer, and stays after it ends if anything
// failed. Successful and cancelled jobs are dismissed automatically.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { baseName } from '@/api/fs'
import { jobsApi } from '@/api/jobs'
import type { Job } from '@/api/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { formatSize } from '@/lib/format'
import { useJobs } from '@/stores/jobs'
import ConflictDialog from './ConflictDialog.vue'

const SHOW_AFTER_MS = 600

const jobs = useJobs()
const now = ref(Date.now())
/** When this client first saw each job (the worker's clock may differ). */
const seen = new Map<string, number>()
const dismissing = new Set<string>()
let timer: ReturnType<typeof setInterval> | undefined

watch(
  () => jobs.jobs,
  (list) => {
    for (const j of list) {
      if (!seen.has(j.id)) seen.set(j.id, Date.now())
      const clean = (j.state === 'done' && j.errorCount === 0) || j.state === 'cancelled'
      if (j.finished && clean && !dismissing.has(j.id)) {
        dismissing.add(j.id)
        void jobsApi.dismiss(j.id).catch(() => {})
      }
    }
    const running = list.some((j) => !j.finished)
    if (running && !timer) timer = setInterval(() => (now.value = Date.now()), 250)
    if (!running && timer) {
      clearInterval(timer)
      timer = undefined
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(timer))

const visible = computed(() =>
  jobs.jobs.filter((j) => {
    if (j.state === 'waiting' || j.state === 'failed') return true
    if (j.finished) return j.errorCount > 0
    return now.value - (seen.get(j.id) ?? now.value) >= SHOW_AFTER_MS
  }),
)

function what(j: Job): string {
  return j.sources.length === 1 ? `“${baseName(j.sources[0]!)}”` : `${j.sources.length} items`
}

function title(j: Job): string {
  const to = j.dest ? ` to “${baseName(j.dest)}”` : ''
  const verb = { copy: 'Copying', move: 'Moving', drop: 'Moving', delete: 'Deleting' }[j.op]
  if (j.finished) {
    const done = { copy: 'Copied', move: 'Moved', drop: 'Moved', delete: 'Deleted' }[j.op]
    return j.state === 'failed' ? `Could not start ${verb.toLowerCase()} ${what(j)}` : `${done} ${what(j)}${to}`
  }
  return `${verb} ${what(j)}${to}`
}

function fraction(j: Job): number | undefined {
  if (j.state === 'preparing') return undefined
  if (j.totalBytes > 0) return Math.min(1, j.doneBytes / j.totalBytes)
  if (j.totalItems > 0) return Math.min(1, j.doneItems / j.totalItems)
  return undefined
}

function timeLeft(j: Job): string {
  const elapsed = (now.value - (seen.get(j.id) ?? now.value)) / 1000
  if (elapsed < 3 || j.doneBytes <= 0 || j.totalBytes <= j.doneBytes) return ''
  const rate = j.doneBytes / elapsed
  const left = (j.totalBytes - j.doneBytes) / rate
  const span =
    left < 60 ? `${Math.ceil(left)} s` : left < 3600 ? `${Math.ceil(left / 60)} min` : `${(left / 3600).toFixed(1)} h`
  return ` — ${span} left (${formatSize(rate)}/s)`
}

function status(j: Job): string {
  switch (j.state) {
    case 'preparing':
      return `Preparing (${j.totalItems} items)`
    case 'waiting':
      return 'Waiting for your answer'
    case 'failed':
      return j.error ?? 'Failed'
    case 'cancelled':
      return 'Cancelled'
    case 'done':
      return `Finished with ${j.errorCount} ${j.errorCount === 1 ? 'error' : 'errors'}`
  }
  if (j.totalBytes > 0) return `${formatSize(j.doneBytes)} of ${formatSize(j.totalBytes)}${timeLeft(j)}`
  return `${j.doneItems} of ${j.totalItems} items`
}

function icon(j: Job): string[] {
  return j.op === 'delete' ? ['user-trash-full', 'user-trash'] : ['system-file-manager', 'folder']
}
</script>

<template>
  <div class="jobs" aria-live="polite">
    <section v-for="j in visible" :key="j.id" class="job" :class="j.state">
      <ThemeIcon :name="icon(j)" :size="32" />
      <div class="body">
        <div class="title">{{ title(j) }}</div>
        <progress v-if="!j.finished" :max="1" :value="fraction(j)" :aria-label="`Progress of ${title(j)}`" />
        <div class="status dim">{{ status(j) }}</div>
        <div v-if="j.current && !j.finished" class="current dim">{{ baseName(j.current) }}</div>
        <details v-if="j.errors.length" class="errors">
          <summary>Show details</summary>
          <ul>
            <li v-for="(e, i) in j.errors" :key="i">
              <span class="path">{{ e.path }}</span>
              <span class="dim">{{ e.error }}</span>
            </li>
            <li v-if="j.errorCount > j.errors.length" class="dim">and {{ j.errorCount - j.errors.length }} more</li>
          </ul>
        </details>
      </div>
      <button
        v-if="!j.finished"
        class="btn flat icon-only"
        title="Cancel"
        @click="jobsApi.cancel(j.id).catch(() => {})"
      >
        <SymbolicIcon name="close" />
      </button>
      <button v-else class="btn" @click="jobsApi.dismiss(j.id).catch(() => {})">Dismiss</button>
    </section>
  </div>
  <ConflictDialog />
</template>

<style scoped>
.jobs {
  position: absolute;
  right: 12px;
  bottom: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 380px;
  max-width: calc(100% - 24px);
  max-height: calc(100% - 24px);
  overflow-y: auto;
  z-index: 60000;
  pointer-events: none;
}
.job {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px;
  border-radius: var(--menu-radius);
  background: var(--menu-bg);
  color: var(--menu-fg);
  box-shadow: var(--menu-shadow);
  border-left: 4px solid var(--accent);
  pointer-events: auto;
}
.job.failed,
.job.done {
  border-left-color: var(--danger);
}
.body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.title {
  font-weight: bold;
  overflow-wrap: anywhere;
}
progress {
  width: 100%;
  height: 8px;
  accent-color: var(--accent);
}
.status,
.current {
  font-size: var(--font-size-small);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.errors summary {
  cursor: default;
  font-size: var(--font-size-small);
}
.errors ul {
  margin: 4px 0 0;
  padding: 0 0 0 16px;
  max-height: 160px;
  overflow-y: auto;
  font-size: var(--font-size-small);
}
.errors li {
  display: flex;
  flex-direction: column;
  margin-bottom: 4px;
  overflow-wrap: anywhere;
}
.btn {
  flex: none;
}
</style>
