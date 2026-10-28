<script setup lang="ts">
// System Monitor (D59), after GNOME's that Mint ships: the processes tab,
// every user's processes by default, refreshed every three seconds while
// the page is visible. Stop, Continue, End and Kill go out as the user, so
// only the user's own processes take them; End and Kill ask first.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from 'reka-ui'
import { ApiError } from '@/api/client'
import { processesApi } from '@/api/processes'
import type { Process, ProcessList, ProcessSignal } from '@/api/types'
import type { AppContext } from '@/apps/types'
import { formatSize } from '@/lib/format'
import {
  nextSort,
  priorityLabel,
  stateLabel,
  visibleProcesses,
  type ProcessView,
  type Sort,
  type SortKey,
} from './processes'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

/** How often the list refreshes, as GNOME System Monitor's default. */
const REFRESH_MS = 3000

const data = ref<ProcessList | null>(null)
const error = ref('')
const view = ref<ProcessView>(
  ctx.args.view === 'mine' || ctx.args.view === 'active' ? (ctx.args.view as ProcessView) : 'all',
)
const query = ref('')
const sort = ref<Sort>({ key: 'cpu', desc: true })
/** The selected process, by PID and start time (a PID can come back as another process). */
const selected = ref<{ pid: number; start: number } | null>(null)

const rows = computed(() =>
  data.value ? visibleProcesses(data.value.processes, view.value, data.value.uid, query.value, sort.value) : [],
)
const current = computed(() =>
  rows.value.find((p) => p.pid === selected.value?.pid && p.start === selected.value?.start),
)
const mine = (p: Process) => p.uid === data.value?.uid

async function refresh() {
  try {
    data.value = await processesApi.list()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (!document.hidden) void refresh()
  }, REFRESH_MS)
})
onBeforeUnmount(() => clearInterval(timer))

function setView(v: ProcessView) {
  view.value = v
  ctx.saveState({ view: v })
}

function sortBy(key: SortKey) {
  sort.value = nextSort(sort.value, key)
}

function ariaSort(key: SortKey) {
  if (sort.value.key !== key) return 'none'
  return sort.value.desc ? 'descending' : 'ascending'
}

const signalNames: Record<ProcessSignal, string> = {
  TERM: 'End',
  KILL: 'Kill',
  STOP: 'Stop',
  CONT: 'Continue',
  HUP: 'Hang Up',
  INT: 'Interrupt',
}

async function send(p: Process, signal: ProcessSignal) {
  if (signal === 'TERM' || signal === 'KILL') {
    const verb = signalNames[signal]
    const self = p.pid === data.value?.self
    const ok = await ctx.confirm({
      title: `${verb} the selected process “${p.name}” (PID: ${p.pid})?`,
      message: self
        ? 'This is Tilde’s own session process: ending it ends your session in every browser, and unsaved work in terminals is lost.'
        : `${signal === 'KILL' ? 'Killing' : 'Ending'} a process may destroy data, break the session or introduce a security risk. Only unresponsive processes should be ${signal === 'KILL' ? 'killed' : 'ended'}.`,
      confirm: `${verb} Process`,
      danger: true,
    })
    if (!ok) return
  }
  try {
    await processesApi.signal(p.pid, p.start, signal)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'gone') ctx.notify(`“${p.name}” has already ended.`)
    else if (e instanceof ApiError && e.status === 403)
      ctx.notify(`“${p.name}” belongs to ${p.user}: only its owner or an administrator can do that.`, 'error')
    else ctx.notify(`Could not ${signalNames[signal].toLowerCase()} “${p.name}”: ${(e as Error).message}`, 'error')
  }
  void refresh()
}

function onKey(e: KeyboardEvent) {
  const p = current.value
  if (!p || (e.target instanceof HTMLInputElement && e.target.type === 'search')) return
  if (e.key === 'Delete' && mine(p)) {
    e.preventDefault()
    void send(p, 'TERM')
  }
}

const columns: { key: SortKey; label: string; numeric?: boolean }[] = [
  { key: 'name', label: 'Process Name' },
  { key: 'user', label: 'User' },
  { key: 'cpu', label: '% CPU', numeric: true },
  { key: 'pid', label: 'ID', numeric: true },
  { key: 'memory', label: 'Memory', numeric: true },
  { key: 'nice', label: 'Priority' },
]
</script>

<template>
  <div class="sysmon" @keydown="onKey">
    <header class="toolbar">
      <div class="views" role="radiogroup" aria-label="Show">
        <button
          v-for="v in [
            ['mine', 'My Processes'],
            ['all', 'All Processes'],
            ['active', 'Active Processes'],
          ] as const"
          :key="v[0]"
          class="btn"
          role="radio"
          :aria-checked="view === v[0]"
          :class="{ on: view === v[0] }"
          @click="setView(v[0])"
        >
          {{ v[1] }}
        </button>
      </div>
      <input v-model="query" class="entry search" type="search" placeholder="Search" aria-label="Search processes" />
    </header>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <ContextMenuRoot :modal="false">
      <ContextMenuTrigger as-child>
        <div class="table-wrap">
          <table aria-label="Processes">
            <thead>
              <tr>
                <th
                  v-for="c in columns"
                  :key="c.key"
                  :class="{ numeric: c.numeric }"
                  :aria-sort="ariaSort(c.key)"
                  scope="col"
                >
                  <button class="sort" @click="sortBy(c.key)">{{ c.label }}</button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="p in rows"
                :key="`${p.pid}:${p.start}`"
                :class="{ selected: current === p }"
                :aria-selected="current === p"
                tabindex="-1"
                :title="p.command || p.name"
                @pointerdown="selected = { pid: p.pid, start: p.start }"
                @contextmenu="selected = { pid: p.pid, start: p.start }"
              >
                <td class="name">
                  {{ p.name }}
                  <span v-if="p.state === 'T' || p.state === 'Z'" class="state">({{ stateLabel(p.state) }})</span>
                </td>
                <td>{{ p.user }}</td>
                <td class="numeric">{{ p.cpu.toFixed(1) }}</td>
                <td class="numeric">{{ p.pid }}</td>
                <td class="numeric">{{ p.memory ? formatSize(p.memory) : 'N/A' }}</td>
                <td>{{ priorityLabel(p.nice) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </ContextMenuTrigger>
      <ContextMenuPortal>
        <ContextMenuContent v-if="current" class="popup-menu">
          <ContextMenuItem
            class="item"
            :disabled="!mine(current) || current.state === 'T'"
            @select="send(current, 'STOP')"
          >
            Stop
          </ContextMenuItem>
          <ContextMenuItem
            class="item"
            :disabled="!mine(current) || current.state !== 'T'"
            @select="send(current, 'CONT')"
          >
            Continue
          </ContextMenuItem>
          <ContextMenuSeparator class="separator" />
          <ContextMenuItem class="item" :disabled="!mine(current)" @select="send(current, 'TERM')">End</ContextMenuItem>
          <ContextMenuItem class="item" :disabled="!mine(current)" @select="send(current, 'KILL')"
            >Kill</ContextMenuItem
          >
        </ContextMenuContent>
      </ContextMenuPortal>
    </ContextMenuRoot>
    <footer class="status">
      <span v-if="data">{{ rows.length }} of {{ data.processes.length }} processes</span>
      <button class="btn" :disabled="!current || !mine(current)" @click="current && send(current, 'TERM')">
        End Process
      </button>
    </footer>
  </div>
</template>

<style scoped>
.sysmon {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
}
.toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px;
  background: var(--headerbar-bg);
  border-bottom: 1px solid var(--border);
}
.views {
  display: flex;
}
.views .btn {
  border-radius: 0;
}
.views .btn:first-child {
  border-radius: var(--radius) 0 0 var(--radius);
}
.views .btn:last-child {
  border-radius: 0 var(--radius) var(--radius) 0;
}
.views .btn.on {
  background: var(--button-active-bg);
  box-shadow: inset 0 0 0 1px var(--accent);
}
.search {
  margin-left: auto;
  width: 200px;
}
.error {
  margin: 0;
  padding: 6px 10px;
  color: var(--danger);
}
.table-wrap {
  flex: 1;
  min-height: 0;
  overflow: auto;
  background: var(--view-bg);
  color: var(--view-fg);
}
table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}
th {
  position: sticky;
  top: 0;
  z-index: 1;
  padding: 0;
  background: var(--window-bg);
  border-bottom: 1px solid var(--border);
  text-align: left;
  font-weight: normal;
}
th:first-child {
  width: 34%;
}
.sort {
  width: 100%;
  padding: 5px 8px;
  border: none;
  background: transparent;
  color: inherit;
  text-align: inherit;
}
.sort:hover {
  background: var(--hover-bg);
}
th[aria-sort='ascending'] .sort::after {
  content: ' ▴';
}
th[aria-sort='descending'] .sort::after {
  content: ' ▾';
}
.numeric {
  text-align: right;
}
td {
  padding: 3px 8px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
tr.selected td {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.state {
  color: var(--view-dim-fg);
}
tr.selected .state {
  color: inherit;
}
.status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  border-top: 1px solid var(--border);
  color: var(--view-dim-fg);
}
</style>
