<script setup lang="ts">
// The panel's network applet (D55), as Cinnamon's: the host's wired and
// Wi-Fi devices from NetworkManager, the Wi-Fi switch, and the networks in
// range to connect to. Hidden without NetworkManager.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger, SwitchRoot, SwitchThumb } from 'reka-ui'
import { networkApi } from '@/api/network'
import type { NetDevice } from '@/api/types'
import SymbolicIcon, { type SymbolicName } from '@/components/SymbolicIcon.vue'
import { useDialogs } from '@/stores/dialogs'
import { signalLevel, useNetwork } from '@/stores/network'
import { useNotices } from '@/stores/notices'
import { connect, disconnect, setWifi, type NetworkHost } from './networkActions'

const net = useNetwork()
const dialogs = useDialogs()
const notices = useNotices()
const host: NetworkHost = {
  confirm: dialogs.confirm,
  prompt: dialogs.prompt,
  notify: notices.push,
  hostname: location.hostname,
}

const open = ref(false)
/** What a request is running for: a device id, a device id and network name, or "wifi". */
const busy = ref<string | null>(null)

onMounted(net.start)
onBeforeUnmount(net.stop)

// Opening the applet shows fresh networks: NetworkManager scans every few
// minutes by itself, and on request.
watch(open, async (v) => {
  if (!v) return
  await net.refresh()
  for (const d of net.devices) {
    if (d.type === 'wifi' && d.state !== 'unavailable') void networkApi.scan(d.id).catch(() => {})
  }
})

const wifiIcon = (strength: number): SymbolicName => `network-wireless-${signalLevel(strength)}`

const iconName = computed<SymbolicName>(() => {
  const i = net.icon
  if (i.kind === 'wired') return 'network-wired'
  if (i.kind === 'wifi') return `network-wireless-${i.level}`
  if (i.kind === 'connecting') return 'network-wireless-0'
  return 'network-offline'
})

const label = computed(() => {
  const up = net.devices.filter((d) => d.state === 'connected')
  const main = up.find((d) => d.default) ?? up[0]
  if (!main) return 'Not connected'
  return main.type === 'wifi' ? `Connected to ${main.connection ?? 'Wi-Fi'}` : 'Wired connection'
})

function title(d: NetDevice) {
  const name = d.type === 'wifi' ? 'Wi-Fi' : 'Wired'
  return net.devices.filter((o) => o.type === d.type).length > 1 ? `${name} (${d.interface})` : name
}

function detail(d: NetDevice) {
  switch (d.state) {
    case 'connected':
      return [d.connection, d.addresses?.[0]].filter(Boolean).join(' · ')
    case 'connecting':
      return 'Connecting…'
    case 'deactivating':
      return 'Disconnecting…'
    case 'failed':
      return 'Connection failed'
    case 'unavailable':
      if (d.type === 'ethernet') return 'Cable unplugged'
      return net.state?.wifiEnabled ? 'Unavailable' : 'Off'
  }
  return 'Disconnected'
}

const canConnect = (d: NetDevice) => d.state === 'disconnected' || d.state === 'failed'
const canDisconnect = (d: NetDevice) => d.state === 'connected' || d.state === 'connecting'

async function run(key: string, fn: () => Promise<void>) {
  if (busy.value) return
  busy.value = key
  try {
    await fn()
  } finally {
    busy.value = null
    void net.refresh()
  }
}
</script>

<template>
  <PopoverRoot v-if="net.visible" v-model:open="open">
    <PopoverTrigger class="applet network" :class="{ active: open }" :title="label" :aria-label="`Network: ${label}`">
      <SymbolicIcon :name="iconName" :size="16" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="network-menu" side="top" align="end" :side-offset="0">
        <section v-for="d in net.devices" :key="d.id" class="device" :aria-label="title(d)">
          <div class="head">
            <SymbolicIcon :name="d.type === 'wifi' ? 'network-wireless-3' : 'network-wired'" :size="16" />
            <div class="what">
              <strong>{{ title(d) }}</strong>
              <span class="detail">{{ detail(d) }}</span>
            </div>
            <button
              v-if="canDisconnect(d)"
              class="btn"
              :disabled="!!busy"
              @click="run(d.id, () => disconnect(host, net.devices, d))"
            >
              Disconnect
            </button>
            <button
              v-else-if="d.type === 'ethernet' && canConnect(d)"
              class="btn"
              :disabled="!!busy"
              @click="run(d.id, () => connect(host, d))"
            >
              {{ busy === d.id ? 'Connecting…' : 'Connect' }}
            </button>
            <SwitchRoot
              v-if="d.type === 'wifi'"
              class="switch"
              aria-label="Wi-Fi"
              :model-value="net.state?.wifiEnabled"
              :disabled="!net.state?.wifiHardware || !!busy"
              @update:model-value="(on: boolean) => run('wifi', () => setWifi(host, net.devices, on))"
            >
              <SwitchThumb class="thumb" />
            </SwitchRoot>
          </div>
          <p v-if="d.type === 'wifi' && !net.state?.wifiHardware" class="note">
            Wi-Fi is off with the computer’s own switch.
          </p>
          <ul
            v-else-if="d.type === 'wifi' && net.state?.wifiEnabled"
            class="networks"
            :aria-label="`${title(d)} networks`"
          >
            <li v-for="n in d.networks" :key="n.ssid">
              <button
                class="network"
                :class="{ active: n.active }"
                :disabled="!!busy || n.active"
                :aria-current="n.active || undefined"
                @click="run(d.id + '\n' + n.ssid, () => connect(host, d, n))"
              >
                <SymbolicIcon :name="wifiIcon(n.strength)" :size="16" />
                <span class="ssid">{{ n.ssid }}</span>
                <span v-if="busy === d.id + '\n' + n.ssid" class="state">Connecting…</span>
                <SymbolicIcon v-else-if="n.active" name="check" :size="14" />
                <span v-if="n.security !== 'none'" class="secured" title="Secured"
                  ><SymbolicIcon name="lock" :size="12"
                /></span>
              </button>
            </li>
            <li v-if="!d.networks?.length" class="note">No networks found</li>
          </ul>
        </section>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.applet.network {
  display: grid;
  place-items: center;
  height: 100%;
  padding: 0 8px;
  border: none;
  background: transparent;
  color: var(--panel-item-fg);
}
.applet.network:hover,
.applet.network.active {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.network-menu {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 320px;
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
.network-menu .head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px;
}
.network-menu .what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.network-menu .detail,
.network-menu .note,
.network-menu .secured,
.network-menu .state {
  color: var(--menu-dim-fg);
}
.network-menu .detail {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.network-menu .note {
  margin: 4px;
}
.network-menu .networks {
  margin: 4px 0 0;
  padding: 4px;
  list-style: none;
  border-radius: var(--menu-radius);
  background: var(--menu-box-bg);
}
.network-menu .network {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  border: none;
  border-radius: var(--menu-radius);
  background: transparent;
  color: inherit;
  text-align: left;
}
.network-menu .network:hover:not(:disabled) {
  background: var(--menu-item-hover-bg);
  color: var(--menu-item-hover-fg);
}
.network-menu .network:disabled {
  opacity: 1;
}
.network-menu .network.active {
  font-weight: bold;
}
.network-menu .ssid {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.network-menu .switch {
  position: relative;
  flex: none;
  width: 42px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: 11px;
  background: var(--switch-bg);
}
.network-menu .switch[data-state='checked'] {
  background: var(--switch-on-bg);
}
.network-menu .switch:disabled {
  opacity: 0.5;
}
.network-menu .thumb {
  display: block;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--switch-thumb);
  transform: translateX(2px);
  transition: transform 0.12s ease-out;
}
.network-menu .thumb[data-state='checked'] {
  transform: translateX(22px);
}
</style>
