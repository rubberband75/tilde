<script setup lang="ts">
// The panel's system monitor applet (D61): CPU and memory over the last
// minute, the figures in its tooltip; a click opens System Monitor's
// Resources tab. It watches the worker's snapshots while the panel shows.
import { computed } from 'vue'
import TimeGraph from '@/components/TimeGraph.vue'
import { percent, share } from '@/lib/rates'
import { formatSize } from '@/lib/format'
import { useResourcesWatch } from '@/lib/resources'
import { useResources } from '@/stores/resources'
import { useWindows } from '@/stores/windows'

useResourcesWatch()
const res = useResources()
const wm = useWindows()

const cpu = computed(() => res.history.map((r) => r.cpu))
const memory = computed(() => res.history.map((r) => share(r.memory.used, r.memory.total)))

const label = computed(() => {
  const r = res.latest
  if (!r) return 'System Monitor'
  return [
    `CPU: ${percent(r.cpu)}`,
    `Memory: ${formatSize(r.memory.used)} of ${formatSize(r.memory.total)} (${percent(share(r.memory.used, r.memory.total))})`,
    r.swap.total ? `Swap: ${formatSize(r.swap.used)} of ${formatSize(r.swap.total)}` : '',
  ]
    .filter(Boolean)
    .join('\n')
})

function open() {
  const id = wm.open('system-monitor', { tab: 'resources' })
  if (id) wm.saveState(id, { tab: 'resources' })
}
</script>

<template>
  <button class="applet resources" :title="label" :aria-label="label.replaceAll('\n', ', ')" @click="open">
    <span class="mini">
      <TimeGraph
        compact
        :max="100"
        label="CPU over the last minute"
        :series="[{ label: 'CPU', slot: 1, values: cpu }]"
      />
    </span>
    <span class="mini">
      <TimeGraph
        compact
        :max="100"
        label="Memory over the last minute"
        :series="[{ label: 'Memory', slot: 1, values: memory }]"
      />
    </span>
  </button>
</template>

<style scoped>
.applet.resources {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 100%;
  padding: 0 8px;
  border: none;
  background: transparent;
}
.applet.resources:hover {
  background: var(--panel-item-hover-bg);
}
.mini {
  display: block;
  width: 36px;
  height: 22px;
  border: 1px solid var(--graph-grid);
  border-radius: 2px;
  overflow: hidden;
}
</style>
