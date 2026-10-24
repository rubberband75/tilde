<script setup lang="ts">
// The panel's list of the user's signed-in browsers (D47). Several may
// share the session (D12); this shows which, where from, whether a tab is
// open, and signs out one left open somewhere else. The number shows
// once there is more than this browser.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { ApiError } from '@/api/client'
import type { ClientInfo } from '@/api/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { ago, describeAgent } from '@/lib/agent'
import { useClients } from '@/stores/clients'
import { useNotices } from '@/stores/notices'

const store = useClients()
const notices = useNotices()
const open = ref(false)
const busy = ref('')
const count = computed(() => store.clients.length)
const label = computed(() =>
  count.value > 1 ? `${count.value} browsers signed in` : 'Signed in from this browser only',
)

function state(c: ClientInfo): string {
  if (c.current) return 'This browser'
  if (c.locked) return 'Locked'
  if (!c.connected) return `Not open, last seen ${ago(c.lastSeen)}`
  return Date.now() - new Date(c.lastInput).getTime() < 120_000 ? 'In use' : `Open, idle ${ago(c.lastInput)}`
}

async function signOut(c: ClientInfo) {
  busy.value = c.id
  try {
    await store.signOut(c.id)
  } catch (e) {
    notices.push(
      `Could not sign out ${describeAgent(c.agent)}: ${e instanceof ApiError ? e.message : 'no answer'}`,
      'error',
    )
  } finally {
    busy.value = ''
  }
}

function onVisible() {
  if (document.visibilityState === 'visible') void store.refresh()
}

watch(open, (o) => o && void store.refresh())
onMounted(() => {
  store.start()
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  store.stop()
  document.removeEventListener('visibilitychange', onVisible)
})
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger class="applet clients" :class="{ active: open }" :title="label" :aria-label="label">
      <SymbolicIcon name="devices" :size="16" />
      <span v-if="count > 1" class="count">{{ count }}</span>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="clients-menu" side="top" align="end" :side-offset="0">
        <h2>Signed-in browsers</h2>
        <ul>
          <li v-for="c in store.clients" :key="c.id" :class="{ current: c.current }">
            <div class="what">
              <span class="name">{{ describeAgent(c.agent) }}</span>
              <span class="detail">{{ c.address }} · {{ state(c) }}</span>
              <span class="detail">Signed in {{ ago(c.since) }}</span>
            </div>
            <button
              v-if="!c.current"
              class="btn"
              :disabled="busy === c.id"
              :aria-label="`Sign out ${describeAgent(c.agent)} at ${c.address}`"
              @click="signOut(c)"
            >
              Sign Out
            </button>
          </li>
        </ul>
        <p v-if="store.others.length === 0" class="only">No other browser is signed in.</p>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.applet.clients {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 100%;
  padding: 0 8px;
  border: none;
  background: transparent;
  color: var(--panel-item-fg);
}
.applet.clients:hover,
.applet.clients.active {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
.count {
  font-size: 12px;
  font-weight: bold;
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.clients-menu {
  width: 340px;
  max-height: calc(100vh - var(--panel-height) - 10px);
  overflow: auto;
  padding: 10px;
  background: var(--menu-bg);
  color: var(--menu-fg);
  border-radius: var(--menu-radius) var(--menu-radius) 0 0;
  box-shadow: var(--menu-shadow);
  z-index: 100000;
  outline: none;
}
.clients-menu h2 {
  margin: 0 0 8px;
  font-size: 1em;
}
.clients-menu ul {
  margin: 0;
  padding: 0;
  list-style: none;
}
.clients-menu li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  border-radius: var(--menu-radius);
}
.clients-menu li.current {
  background: var(--menu-box-bg);
}
.clients-menu .what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.clients-menu .detail,
.clients-menu .only {
  color: var(--menu-dim-fg);
  overflow-wrap: anywhere;
}
.clients-menu .only {
  margin: 8px 0 0;
}
</style>
