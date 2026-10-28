<script setup lang="ts">
// System Monitor's Services tab (D60): systemd's services, the system's or
// the user's own, with Start, Stop and Restart. systemd and polkit decide:
// a system service usually asks for an administrator's password, through
// Tilde's polkit dialog, which comes up by itself while a request waits.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ContextMenuContent, ContextMenuItem, ContextMenuPortal, ContextMenuRoot, ContextMenuTrigger } from 'reka-ui'
import { ApiError } from '@/api/client'
import { servicesApi } from '@/api/services'
import type { Service, ServiceAction, ServiceScope } from '@/api/types'
import type { AppContext } from '@/apps/types'
import {
  bootLabel,
  canStart,
  canStop,
  serviceName,
  serviceState,
  visibleServices,
  type ServiceSortKey,
} from './services'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

/** Services change less than processes; their list is longer to make. */
const REFRESH_MS = 5000

const scope = ref<ServiceScope>(ctx.args.serviceScope === 'user' ? 'user' : 'system')
const list = ref<Service[]>([])
const available = ref(true)
const loaded = ref(false)
const error = ref('')
const query = ref('')
const sortKey = ref<ServiceSortKey>('name')
const desc = ref(false)
const selected = ref<string | null>(null)
/** The service a request is running for, and what it does. */
const busy = ref<{ name: string; action: ServiceAction } | null>(null)

const rows = computed(() => visibleServices(list.value, query.value, sortKey.value, desc.value))
const current = computed(() => rows.value.find((s) => s.name === selected.value))
const failed = computed(() => list.value.filter((s) => s.active === 'failed').length)

async function refresh() {
  const at = scope.value
  try {
    const res = await servicesApi.list(at)
    if (at !== scope.value) return
    list.value = res.services
    available.value = res.available
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loaded.value = true
  }
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (!document.hidden && !busy.value) void refresh()
  }, REFRESH_MS)
})
onBeforeUnmount(() => clearInterval(timer))

watch(scope, (s) => {
  ctx.saveState({ serviceScope: s })
  list.value = []
  loaded.value = false
  selected.value = null
  void refresh()
})

function sortBy(key: ServiceSortKey) {
  if (sortKey.value === key) desc.value = !desc.value
  else {
    sortKey.value = key
    desc.value = false
  }
}

function ariaSort(key: ServiceSortKey) {
  if (sortKey.value !== key) return 'none'
  return desc.value ? 'descending' : 'ascending'
}

const verbs: Record<ServiceAction, string> = { start: 'Start', stop: 'Stop', restart: 'Restart' }

async function act(s: Service, action: ServiceAction) {
  if (busy.value) return
  const name = serviceName(s)
  // Tilde itself: every browser's session ends with it.
  if (scope.value === 'system' && s.name === 'tilde.service' && action !== 'start') {
    const ok = await ctx.confirm({
      title: `${verbs[action]} Tilde?`,
      message:
        action === 'stop'
          ? 'This ends every Tilde session on this computer, this one too, and Tilde stays off until someone starts it.'
          : 'This ends every Tilde session on this computer, this one too; you can log in again once it is back.',
      confirm: verbs[action],
      danger: true,
    })
    if (!ok) return
  }
  busy.value = { name: s.name, action }
  try {
    const res = await servicesApi.act(scope.value, s.name, action)
    const want = action === 'stop' ? ['inactive', 'failed'] : ['active']
    if (!want.includes(res.active)) {
      ctx.notify(
        res.active === 'failed'
          ? `“${name}” failed to ${action}.`
          : `“${name}” is ${serviceState({ ...s, active: res.active, sub: res.sub }).toLowerCase()}.`,
        res.active === 'failed' ? 'error' : 'info',
      )
    }
  } catch (e) {
    const msg = e instanceof ApiError && e.code === 'not_authorized' ? 'not allowed' : (e as Error).message
    ctx.notify(`Could not ${action} “${name}”: ${msg}`, 'error')
  } finally {
    busy.value = null
    void refresh()
  }
}

function stateText(s: Service) {
  if (busy.value?.name === s.name) {
    return { start: 'Starting…', stop: 'Stopping…', restart: 'Restarting…' }[busy.value.action]
  }
  return serviceState(s)
}

const columns: { key: ServiceSortKey | null; label: string }[] = [
  { key: 'name', label: 'Service' },
  { key: null, label: 'Description' },
  { key: 'state', label: 'State' },
  { key: 'boot', label: 'Starts at Boot' },
]
</script>

<template>
  <div class="services">
    <header class="toolbar">
      <div class="views" role="radiogroup" aria-label="Services of">
        <button
          v-for="v in [
            ['system', 'System'],
            ['user', 'My Services'],
          ] as const"
          :key="v[0]"
          class="btn"
          role="radio"
          :aria-checked="scope === v[0]"
          :class="{ on: scope === v[0] }"
          @click="scope = v[0]"
        >
          {{ v[1] }}
        </button>
      </div>
      <input v-model="query" class="entry search" type="search" placeholder="Search" aria-label="Search services" />
    </header>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-else-if="loaded && !available" class="empty">
      {{ scope === 'user' ? 'Your own systemd manager is not running.' : 'systemd is not running on this computer.' }}
    </p>
    <ContextMenuRoot v-else :modal="false">
      <ContextMenuTrigger as-child>
        <div class="table-wrap">
          <table aria-label="Services">
            <thead>
              <tr>
                <th v-for="c in columns" :key="c.label" :aria-sort="c.key ? ariaSort(c.key) : undefined" scope="col">
                  <button v-if="c.key" class="sort" @click="sortBy(c.key)">{{ c.label }}</button>
                  <span v-else class="sort">{{ c.label }}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="s in rows"
                :key="s.name"
                :class="{ selected: current === s, failed: s.active === 'failed' }"
                :aria-selected="current === s"
                :title="s.name"
                @pointerdown="selected = s.name"
                @contextmenu="selected = s.name"
              >
                <td>{{ serviceName(s) }}</td>
                <td class="dim">{{ s.description }}</td>
                <td class="state">{{ stateText(s) }}</td>
                <td>{{ bootLabel(s) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </ContextMenuTrigger>
      <ContextMenuPortal>
        <ContextMenuContent v-if="current" class="popup-menu">
          <ContextMenuItem class="item" :disabled="!!busy || !canStart(current)" @select="act(current, 'start')">
            Start
          </ContextMenuItem>
          <ContextMenuItem class="item" :disabled="!!busy || !canStop(current)" @select="act(current, 'stop')">
            Stop
          </ContextMenuItem>
          <ContextMenuItem
            class="item"
            :disabled="!!busy || current.load === 'masked'"
            @select="act(current, 'restart')"
          >
            Restart
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenuPortal>
    </ContextMenuRoot>
    <footer class="status">
      <span v-if="loaded && available">
        {{ rows.length }} of {{ list.length }} services<template v-if="failed">, {{ failed }} failed</template>
      </span>
      <span class="actions">
        <button
          class="btn"
          :disabled="!current || !!busy || !canStart(current)"
          @click="current && act(current, 'start')"
        >
          Start
        </button>
        <button
          class="btn"
          :disabled="!current || !!busy || !canStop(current)"
          @click="current && act(current, 'stop')"
        >
          Stop
        </button>
        <button
          class="btn"
          :disabled="!current || !!busy || current.load === 'masked'"
          @click="current && act(current, 'restart')"
        >
          Restart
        </button>
      </span>
    </footer>
  </div>
</template>

<style scoped>
.services {
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
.error,
.empty {
  margin: 0;
  padding: 10px;
}
.error {
  color: var(--danger);
}
.empty {
  color: var(--view-dim-fg);
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
th:nth-child(1) {
  width: 26%;
}
th:nth-child(3) {
  width: 14%;
}
th:nth-child(4) {
  width: 15%;
}
.sort {
  display: block;
  width: 100%;
  padding: 5px 8px;
  border: none;
  background: transparent;
  color: inherit;
  text-align: inherit;
}
button.sort:hover {
  background: var(--hover-bg);
}
th[aria-sort='ascending'] .sort::after {
  content: ' ▴';
}
th[aria-sort='descending'] .sort::after {
  content: ' ▾';
}
td {
  padding: 3px 8px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
td.dim {
  color: var(--view-dim-fg);
}
tr.failed .state {
  color: var(--danger);
  font-weight: bold;
}
tr.selected td {
  background: var(--selection-bg);
  color: var(--selection-fg);
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
.actions {
  display: flex;
  gap: 6px;
  margin-left: auto;
}
</style>
