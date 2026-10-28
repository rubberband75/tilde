<script setup lang="ts">
// System Monitor's Resources tab (D61), as GNOME's: CPU, memory and swap,
// network and disk over the last minute, with the figures now beside each
// graph. The snapshots come over the events socket while the tab shows.
import { computed } from 'vue'
import type { AppContext } from '@/apps/types'
import TimeGraph from '@/components/TimeGraph.vue'
import { formatSize } from '@/lib/format'
import { percent, rate, share } from '@/lib/rates'
import { useResourcesWatch } from '@/lib/resources'
import { useResources } from '@/stores/resources'

defineProps<{ ctx: AppContext }>()

useResourcesWatch()
const res = useResources()
const h = computed(() => res.history)
const now = computed(() => res.latest)

const cpu = computed(() => h.value.map((r) => r.cpu))
const memory = computed(() => h.value.map((r) => share(r.memory.used, r.memory.total)))
const swap = computed(() => h.value.map((r) => share(r.swap.used, r.swap.total)))
const hasSwap = computed(() => (now.value?.swap.total ?? 0) > 0)
</script>

<template>
  <div class="resources">
    <p v-if="!now" class="waiting">Measuring…</p>
    <template v-else>
      <section aria-labelledby="res-cpu">
        <header>
          <h3 id="res-cpu">CPU</h3>
          <span class="figure">{{ percent(now.cpu) }}</span>
        </header>
        <div class="plot">
          <TimeGraph
            label="CPU use over the last minute"
            :max="100"
            :format="percent"
            :series="[{ label: 'CPU', slot: 1, values: cpu }]"
          />
        </div>
        <ul class="cores" aria-label="Each CPU">
          <li v-for="(c, i) in now.cores" :key="i">
            <span>CPU{{ i + 1 }}</span
            ><span class="value">{{ percent(c) }}</span>
          </li>
        </ul>
      </section>

      <section aria-labelledby="res-mem">
        <header>
          <h3 id="res-mem">Memory and Swap</h3>
        </header>
        <div class="plot">
          <TimeGraph
            label="Memory and swap use over the last minute"
            :max="100"
            :format="percent"
            :series="
              hasSwap
                ? [
                    { label: 'Memory', slot: 1, values: memory },
                    { label: 'Swap', slot: 2, values: swap },
                  ]
                : [{ label: 'Memory', slot: 1, values: memory }]
            "
          />
        </div>
        <ul class="legend">
          <li>
            <span class="swatch slot-1" />Memory
            <span class="value"
              >{{ formatSize(now.memory.used) }} ({{ percent(share(now.memory.used, now.memory.total)) }}) of
              {{ formatSize(now.memory.total) }}</span
            >
            <span v-if="now.memory.cache" class="dim">Cache {{ formatSize(now.memory.cache) }}</span>
          </li>
          <li v-if="hasSwap">
            <span class="swatch slot-2" />Swap
            <span class="value"
              >{{ formatSize(now.swap.used) }} ({{ percent(share(now.swap.used, now.swap.total)) }}) of
              {{ formatSize(now.swap.total) }}</span
            >
          </li>
          <li v-else class="dim">No swap</li>
        </ul>
      </section>

      <section aria-labelledby="res-net">
        <header>
          <h3 id="res-net">Network</h3>
        </header>
        <div class="plot">
          <TimeGraph
            label="Network traffic over the last minute"
            :format="rate"
            :series="[
              { label: 'Receiving', slot: 1, values: h.map((r) => r.netReceive) },
              { label: 'Sending', slot: 2, values: h.map((r) => r.netSend) },
            ]"
          />
        </div>
        <ul class="legend">
          <li>
            <span class="swatch slot-1" />Receiving <span class="value">{{ rate(now.netReceive) }}</span>
            <span class="dim">Total {{ formatSize(now.netReceived) }}</span>
          </li>
          <li>
            <span class="swatch slot-2" />Sending <span class="value">{{ rate(now.netSend) }}</span>
            <span class="dim">Total {{ formatSize(now.netSent) }}</span>
          </li>
        </ul>
      </section>

      <section aria-labelledby="res-disk">
        <header>
          <h3 id="res-disk">Disk</h3>
        </header>
        <div class="plot">
          <TimeGraph
            label="Disk reads and writes over the last minute"
            :format="rate"
            :series="[
              { label: 'Reading', slot: 1, values: h.map((r) => r.diskRead) },
              { label: 'Writing', slot: 2, values: h.map((r) => r.diskWrite) },
            ]"
          />
        </div>
        <ul class="legend">
          <li>
            <span class="swatch slot-1" />Reading <span class="value">{{ rate(now.diskRead) }}</span>
          </li>
          <li>
            <span class="swatch slot-2" />Writing <span class="value">{{ rate(now.diskWrite) }}</span>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.resources {
  display: flex;
  flex-direction: column;
  gap: 14px;
  height: 100%;
  overflow: auto;
  padding: 12px 16px;
  background: var(--window-bg);
  color: var(--window-fg);
}
.waiting {
  margin: auto;
  color: var(--view-dim-fg);
}
header {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 4px;
}
h3 {
  margin: 0;
  font-size: 1em;
}
.figure {
  color: var(--view-dim-fg);
  font-variant-numeric: tabular-nums;
}
.plot {
  height: 90px;
  padding: 4px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--view-bg);
}
ul {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  margin: 6px 0 0;
  padding: 0;
  list-style: none;
}
li {
  display: flex;
  align-items: center;
  gap: 6px;
}
.value {
  font-variant-numeric: tabular-nums;
}
.dim {
  color: var(--view-dim-fg);
}
.cores li {
  min-width: 90px;
  color: var(--view-dim-fg);
}
.cores .value {
  color: var(--window-fg);
}
.swatch {
  width: 12px;
  height: 3px;
  border-radius: 2px;
}
.swatch.slot-1 {
  background: var(--series-1);
}
.swatch.slot-2 {
  background: var(--series-2);
}
</style>
