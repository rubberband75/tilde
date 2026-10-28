<script setup lang="ts">
// System Monitor (D59, D60, D61), after GNOME's that Mint ships: tabs for
// processes, resources and services. Only the tab shown lists anything.
import { ref, watch } from 'vue'
import type { AppContext } from '@/apps/types'
import ProcessesView from './ProcessesView.vue'
import ResourcesView from './ResourcesView.vue'
import ServicesView from './ServicesView.vue'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

type Tab = 'processes' | 'resources' | 'services'
const asTab = (v: unknown): Tab => (v === 'services' || v === 'resources' ? v : 'processes')
const tab = ref<Tab>(asTab(ctx.args.tab))
// The panel applet opens the Resources tab, in a window already open too.
watch(
  () => ctx.args.tab,
  (t) => {
    tab.value = asTab(t)
  },
)

function show(t: Tab) {
  tab.value = t
  ctx.saveState({ tab: t })
}
</script>

<template>
  <div class="sysmon-app">
    <nav class="tabs" role="tablist" aria-label="System Monitor">
      <button
        v-for="t in [
          ['processes', 'Processes'],
          ['resources', 'Resources'],
          ['services', 'Services'],
        ] as const"
        :key="t[0]"
        class="tab"
        role="tab"
        :aria-selected="tab === t[0]"
        :class="{ on: tab === t[0] }"
        @click="show(t[0])"
      >
        {{ t[1] }}
      </button>
    </nav>
    <ProcessesView v-if="tab === 'processes'" class="pane" :ctx="ctx" />
    <ResourcesView v-else-if="tab === 'resources'" class="pane" :ctx="ctx" />
    <ServicesView v-else class="pane" :ctx="ctx" />
  </div>
</template>

<style scoped>
.sysmon-app {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
}
.tabs {
  display: flex;
  justify-content: center;
  gap: 2px;
  padding: 6px 6px 0;
  background: var(--headerbar-bg);
}
.tab {
  padding: 5px 16px;
  border: none;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--view-dim-fg);
}
.tab:hover {
  color: var(--window-fg);
}
.tab.on {
  border-bottom-color: var(--accent);
  color: var(--window-fg);
}
.pane {
  flex: 1;
  min-height: 0;
}
</style>
