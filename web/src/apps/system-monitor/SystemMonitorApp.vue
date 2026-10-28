<script setup lang="ts">
// System Monitor (D59, D60), after GNOME's that Mint ships: a tab for
// processes and one for services. Only the tab shown lists anything.
import { ref } from 'vue'
import type { AppContext } from '@/apps/types'
import ProcessesView from './ProcessesView.vue'
import ServicesView from './ServicesView.vue'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

type Tab = 'processes' | 'services'
const tab = ref<Tab>(ctx.args.tab === 'services' ? 'services' : 'processes')

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
