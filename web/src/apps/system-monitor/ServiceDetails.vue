<script setup lang="ts">
// The selected service (D62): whether it starts at boot, and its last
// journal lines, as the user may read them. The journal reloads when the
// service changes state, not each time the list refreshes; a reader who
// has scrolled up stays at the line they were reading, and one at the end
// follows new lines, as in a terminal.
import { computed, nextTick, ref, watch } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { ApiError } from '@/api/client'
import { servicesApi } from '@/api/services'
import type { JournalEntry, Service, ServiceScope } from '@/api/types'
import type { AppContext } from '@/apps/types'
import { bootNote, canSwitchBoot, journalTime, serviceName, serviceState } from './services'

const props = defineProps<{ ctx: AppContext; scope: ServiceScope; service: Service }>()
const emit = defineEmits<{ changed: [] }>()

const entries = ref<JournalEntry[]>([])
const limited = ref(false)
const error = ref('')
const switching = ref(false)
const log = ref<HTMLElement | null>(null)

/** Within this many pixels of the end counts as reading the end. */
const FOLLOW_SLACK = 24

/** Names a line across reloads: older lines drop off as new ones come. */
const lineKey = (e: JournalEntry) => `${e.time}\u0000${e.message}`

/** The first line shown, and how far above the top of the view it starts. */
function topLine(el: HTMLElement): { key: string; offset: number } | null {
  for (const row of el.querySelectorAll<HTMLElement>('.entry')) {
    if (row.offsetTop + row.offsetHeight > el.scrollTop) {
      return { key: row.dataset.key ?? '', offset: el.scrollTop - row.offsetTop }
    }
  }
  return null
}

/**
 * Reloads the journal. `fresh` (another service): start at the end.
 * Otherwise stay where the reader is: at the end, follow it; scrolled up,
 * keep the line at the top of the view there.
 */
async function loadJournal(fresh: boolean) {
  const { scope, service } = props
  try {
    const res = await servicesApi.journal(scope, service.name)
    if (service.name !== props.service.name) return
    const el = log.value
    const follow = fresh || !el || el.scrollHeight - el.scrollTop - el.clientHeight <= FOLLOW_SLACK
    const anchor = !follow && el ? topLine(el) : null
    entries.value = res.entries
    limited.value = res.limited
    error.value = ''
    await nextTick()
    const now = log.value
    if (!now) return
    if (follow) {
      now.scrollTop = now.scrollHeight
      return
    }
    // By comparing keys, not a selector: CSS.escape changes the key's NUL.
    const row = anchor && [...now.querySelectorAll<HTMLElement>('.entry')].find((r) => r.dataset.key === anchor.key)
    // Gone off the top: the reader was among the oldest lines, so the top.
    now.scrollTop = row ? row.offsetTop + anchor.offset : 0
  } catch (e) {
    error.value = (e as Error).message
  }
}

// The list refreshes every few seconds with new objects for the same
// services: only another service, or a change of state, reloads.
const watched = computed(() => `${props.scope}\n${props.service.name}`)
const state = computed(() => `${props.service.active}\n${props.service.sub}`)
watch(watched, () => void loadJournal(true), { immediate: true })
watch(state, () => void loadJournal(false))

async function setBoot(on: boolean) {
  switching.value = true
  try {
    await servicesApi.setBoot(props.scope, props.service.name, on)
  } catch (e) {
    const msg = e instanceof ApiError && e.code === 'not_authorized' ? 'not allowed' : (e as Error).message
    props.ctx.notify(`Could not ${on ? 'enable' : 'disable'} “${serviceName(props.service)}”: ${msg}`, 'error')
  } finally {
    switching.value = false
    emit('changed')
  }
}
</script>

<template>
  <section class="details" :aria-label="`${serviceName(service)} details`">
    <header>
      <div class="what">
        <strong>{{ serviceName(service) }}</strong>
        <span>{{ service.description }}</span>
      </div>
      <span class="state" :class="{ failed: service.active === 'failed' }">{{ serviceState(service) }}</span>
      <label class="boot" :title="canSwitchBoot(service) ? undefined : bootNote(service)">
        Start at boot
        <SwitchRoot
          class="switch"
          :model-value="service.boot === 'enabled'"
          :disabled="!canSwitchBoot(service) || switching"
          @update:model-value="(on: boolean) => setBoot(on)"
        >
          <SwitchThumb class="thumb" />
        </SwitchRoot>
      </label>
    </header>
    <p v-if="!canSwitchBoot(service)" class="note">{{ bootNote(service) }}</p>
    <div ref="log" class="log" role="log" :aria-label="`${serviceName(service)} journal`">
      <p v-if="error" class="note">{{ error }}</p>
      <p v-else-if="!entries.length" class="note">
        {{ limited ? 'No log lines you may read.' : 'No log lines.' }}
      </p>
      <div
        v-for="(e, i) in entries"
        :key="i"
        class="entry"
        :data-key="lineKey(e)"
        :class="{ error: e.priority <= 3, warning: e.priority === 4 }"
      >
        <time>{{ journalTime(e.time) }}</time>
        <span class="msg">{{ e.message }}</span>
      </div>
    </div>
    <p v-if="limited" class="note">
      Only your own logs are shown: the system’s need membership of the adm or systemd-journal group.
    </p>
  </section>
</template>

<style scoped>
.details {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-top: 1px solid var(--border);
  background: var(--window-bg);
}
header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px 4px;
}
.what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.what span,
.note {
  color: var(--view-dim-fg);
}
.what strong,
.what span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.state.failed,
.entry.error .msg {
  color: var(--danger);
}
.boot {
  display: flex;
  align-items: center;
  gap: 8px;
}
.note {
  margin: 0;
  padding: 0 10px 6px;
}
.log {
  flex: 1;
  min-height: 0;
  margin: 4px 10px 8px;
  padding: 4px 0;
  overflow: auto;
  border: 1px solid var(--border);
  background: var(--view-bg);
  font-family: var(--font-mono);
  font-size: 12px;
}
.log .note {
  padding: 4px 8px;
}
.entry {
  display: flex;
  gap: 10px;
  padding: 1px 8px;
}
.entry time {
  flex: none;
  color: var(--view-dim-fg);
}
.entry .msg {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.entry.warning .msg {
  font-weight: bold;
}
</style>
