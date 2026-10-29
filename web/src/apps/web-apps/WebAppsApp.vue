<script setup lang="ts">
// Web Apps (D66, D76): the user's programs listening on TCP ports, to open
// in a window. Each opens from Tilde's app port, another origin, so its
// pages never run as Tilde.
import { computed, onMounted, ref } from 'vue'
import { appsApi } from '@/api/apps'
import type { LocalApp } from '@/api/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

const apps = ref<LocalApp[]>([])
const loading = ref(false)
const error = ref('')
/** Show ports that did not answer HTTP too. */
const all = ref(false)
const shown = computed(() => (all.value ? apps.value : apps.value.filter((a) => a.http)))
const hidden = computed(() => apps.value.length - shown.value.length)

async function refresh() {
  loading.value = true
  try {
    apps.value = await appsApi.list()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}
onMounted(refresh)

const local = (a: LocalApp) => a.address === '127.0.0.1' || a.address === '::1'

function open(a: LocalApp) {
  ctx.launch('web-view', { port: a.port, title: a.title || a.process || '' })
}

// A port typed in: for apps the list does not show (another user's are
// refused by the worker).
const typed = ref('')
const typedPort = computed(() => {
  const n = Number(typed.value)
  return Number.isInteger(n) && n >= 1 && n <= 65535 ? n : null
})
function openTyped() {
  if (typedPort.value) ctx.launch('web-view', { port: typedPort.value })
}
</script>

<template>
  <div class="web-apps">
    <div class="toolbar">
      <button class="btn" :disabled="loading" title="Refresh" @click="refresh">
        <SymbolicIcon name="refresh" :size="16" />
      </button>
      <label class="check"><input v-model="all" type="checkbox" /> Show every port</label>
      <span class="spacer" />
      <form class="open-port" @submit.prevent="openTyped">
        <input v-model="typed" class="entry" inputmode="numeric" placeholder="Port" aria-label="Port to open" />
        <button class="btn" :disabled="!typedPort">Open</button>
      </form>
    </div>
    <p v-if="error" class="note error">{{ error }}</p>
    <p v-else-if="!loading && !shown.length" class="note">
      None of your programs serves web pages now. Start one (Jupyter, code-server, a development server), then refresh.
    </p>
    <ul v-else class="list" aria-label="Web apps">
      <li v-for="a in shown" :key="a.port">
        <div class="what">
          <strong>{{ a.title || a.process || `Port ${a.port}` }}</strong>
          <span class="detail">
            Port {{ a.port }}<template v-if="a.process"> · {{ a.process }}</template>
            <template v-if="!local(a)"> · also reachable from the network ({{ a.address }})</template>
            <template v-if="!a.http"> · not a web server</template>
          </span>
        </div>
        <button class="btn" @click="open(a)">Open</button>
      </li>
    </ul>
    <p v-if="hidden && !all" class="note">
      {{ hidden }} more {{ hidden === 1 ? 'port does' : 'ports do' }} not answer as a web server.
    </p>
    <p class="note">
      Apps open from a separate address (Tilde’s app port), so their pages cannot act as Tilde. But the apps you open
      share that address: one app’s pages can reach your other apps. Keep apps that matter behind their own login
      (Jupyter’s token, code-server’s password), and do not open apps you do not trust here.
    </p>
    <p class="note">An app that builds absolute links needs its base path set; see an app window’s menu.</p>
  </div>
</template>

<style scoped>
.web-apps {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
  padding: 10px 12px;
  overflow: auto;
  background: var(--window-bg);
  color: var(--window-fg);
}
.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
}
.spacer {
  flex: 1;
}
.check {
  display: flex;
  align-items: center;
  gap: 6px;
}
.open-port {
  display: flex;
  gap: 6px;
}
.open-port .entry {
  width: 7em;
}
.list {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--view-bg);
}
.list li {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
}
.list li + li {
  border-top: 1px solid var(--border);
}
.what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.what strong {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.detail,
.note {
  color: var(--view-dim-fg);
}
.note {
  margin: 0;
}
.note.error {
  color: var(--danger);
}
</style>
