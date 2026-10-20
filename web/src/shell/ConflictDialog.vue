<script setup lang="ts">
// Asks what to do when a job finds an existing item: Replace (files), Merge
// (folders), Skip, or Rename, optionally for all later conflicts of the same
// kind. Not modal: the rest of the desktop stays usable while a job waits.
// Every client of the user shows it; the first answer wins (D31).
import { computed, nextTick, ref, watch } from 'vue'
import { ApiError } from '@/api/client'
import { baseName, dirName } from '@/api/fs'
import { jobsApi } from '@/api/jobs'
import type { ConflictAction, Entry } from '@/api/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { formatDate, formatSize } from '@/lib/format'
import { useJobs } from '@/stores/jobs'
import { useNotices } from '@/stores/notices'

const jobs = useJobs()
const notices = useNotices()
const job = computed(() => jobs.conflicted)
const conflict = computed(() => job.value?.conflict)

const el = ref<HTMLElement | null>(null)
const renaming = ref(false)
const newName = ref('')
const applyAll = ref(false)
const busy = ref(false)

const canMerge = computed(() => !!conflict.value?.actions.includes('merge'))
const canReplace = computed(() => !!conflict.value?.actions.includes('replace'))
/** A file cannot replace a folder: renaming is the only way to keep it. */
const renameOnly = computed(() => !canMerge.value && !canReplace.value)

/** Where keyboard focus was before the dialog opened; it goes back there. */
let returnFocus: HTMLElement | null = null

watch(
  () => (conflict.value ? `${job.value!.id}:${conflict.value.id}` : ''),
  (key, prev) => {
    renaming.value = renameOnly.value
    newName.value = conflict.value?.suggested ?? ''
    applyAll.value = false
    busy.value = false
    if (key && !prev) {
      const active = document.activeElement
      returnFocus = active instanceof HTMLElement && !el.value?.contains(active) ? active : null
    }
    if (key) {
      // The default answer, as in GTK: Replace or Merge.
      void nextTick(() =>
        (
          el.value?.querySelector<HTMLElement>('.btn.suggested') ?? el.value?.querySelector<HTMLElement>('.btn')
        )?.focus(),
      )
    } else if (prev) {
      if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
      returnFocus = null
    }
  },
  { immediate: true },
)

const primary = computed<ConflictAction | null>(() => {
  if (renaming.value) return 'rename'
  if (canMerge.value) return 'merge'
  return canReplace.value ? 'replace' : null
})
const primaryLabel = computed(
  () => ({ rename: 'Rename', merge: 'Merge', replace: 'Replace', skip: 'Skip' })[primary.value ?? 'skip'],
)

const title = computed(() => {
  const c = conflict.value
  if (!c) return ''
  const name = baseName(c.dest.path)
  if (canMerge.value) return `Merge folder “${name}”?`
  if (renameOnly.value) return `A folder named “${name}” already exists`
  return `Replace file “${name}”?`
})

const message = computed(() => {
  const c = conflict.value
  if (!c) return ''
  const where = baseName(dirName(c.dest.path))
  if (canMerge.value) {
    return `Another folder with the same name already exists in “${where}”. Merging asks before replacing any files in it.`
  }
  if (renameOnly.value) return `It is in “${where}”. A file cannot replace a folder: skip it or give it another name.`
  return `Another file with the same name already exists in “${where}”. Replacing it will overwrite its content.`
})

function details(e: Entry): string {
  const date = `modified ${formatDate(e.mtime)}`
  return e.type === 'dir' ? date : `${formatSize(e.size)}, ${date}`
}

async function answer(action: ConflictAction) {
  const j = job.value
  const c = conflict.value
  if (!j || !c || busy.value) return
  const name = newName.value.trim()
  if (action === 'rename' && (!name || name.includes('/') || name === '.' || name === '..')) {
    notices.push('Enter a valid new name.', 'error')
    return
  }
  busy.value = true
  try {
    await jobsApi.resolve(j.id, {
      conflict: c.id,
      action,
      name: action === 'rename' ? name : undefined,
      applyToAll: applyAll.value,
    })
  } catch (e) {
    // "stale": another client answered first; the next event updates us.
    if (!(e instanceof ApiError && e.code === 'stale')) {
      notices.push(`Could not answer: ${(e as Error).message}`, 'error')
      busy.value = false
    }
  }
}

function cancelJob() {
  if (job.value) void jobsApi.cancel(job.value.id).catch(() => {})
}
</script>

<template>
  <div
    v-if="job && conflict"
    ref="el"
    class="dialog conflict"
    role="alertdialog"
    aria-labelledby="conflict-title"
    @keydown.esc.stop="cancelJob"
  >
    <h2 id="conflict-title" class="title">{{ title }}</h2>
    <p class="message">{{ message }}</p>
    <div class="compare">
      <div class="side">
        <ThemeIcon :name="iconsForEntry(conflict.dest)" :size="48" />
        <div>
          <strong>Original {{ conflict.dest.type === 'dir' ? 'folder' : 'file' }}</strong>
          <span class="dim">{{ details(conflict.dest) }}</span>
        </div>
      </div>
      <div class="side">
        <ThemeIcon :name="iconsForEntry(conflict.source)" :size="48" />
        <div>
          <strong>{{ canMerge ? 'Merge with' : canReplace ? 'Replace with' : 'Incoming file' }}</strong>
          <span class="dim">{{ details(conflict.source) }}</span>
        </div>
      </div>
    </div>
    <label class="check">
      <input v-model="renaming" type="checkbox" :disabled="renameOnly || busy" />
      Select a new name for the destination
    </label>
    <input
      v-if="renaming"
      v-model="newName"
      class="entry name"
      :disabled="busy"
      aria-label="New name"
      @keydown.enter.prevent="answer('rename')"
    />
    <label class="check">
      <input v-model="applyAll" type="checkbox" :disabled="busy" />
      Apply this action to all {{ canMerge ? 'folders' : 'files' }}
    </label>
    <div class="actions">
      <button class="btn" :disabled="busy" @click="cancelJob">Cancel</button>
      <button class="btn" :disabled="busy" @click="answer('skip')">Skip</button>
      <button v-if="primary" class="btn suggested" :disabled="busy" @click="answer(primary)">
        {{ primaryLabel }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.compare {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 14px;
}
.side {
  display: flex;
  align-items: center;
  gap: 12px;
}
.side div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.check {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 6px 0;
}
.check input {
  accent-color: var(--accent);
}
.name {
  width: 100%;
  margin: 2px 0 6px;
}
</style>
