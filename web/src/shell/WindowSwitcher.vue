<script setup lang="ts">
// Alt+Tab's switcher (D53): the windows as icons, most recently used first,
// with the chosen one's title underneath. The keys are in shell/keyboard.ts;
// a click picks a window too.
import ThemeIcon from '@/components/ThemeIcon.vue'
import { useSwitcher } from '@/stores/switcher'

const switcher = useSwitcher()
</script>

<template>
  <div v-if="switcher.active" class="switcher" role="listbox" aria-label="Windows">
    <div class="items">
      <button
        v-for="w in switcher.items"
        :key="w.id"
        class="item"
        role="option"
        :aria-selected="w.id === switcher.selectedId"
        :class="{ selected: w.id === switcher.selectedId, minimized: w.state === 'minimized' }"
        :title="w.title"
        @pointerenter="switcher.select(w.id)"
        @click="switcher.commit()"
      >
        <ThemeIcon :name="w.icon" :size="48" />
      </button>
    </div>
    <div class="title">{{ switcher.items.find((w) => w.id === switcher.selectedId)?.title }}</div>
  </div>
</template>

<style scoped>
.switcher {
  position: fixed;
  left: 50%;
  top: 45%;
  transform: translate(-50%, -50%);
  max-width: calc(100vw - 32px);
  padding: 14px;
  border: 1px solid var(--popup-border);
  border-radius: var(--wm-radius);
  background: var(--popup-bg);
  color: var(--window-fg);
  box-shadow: var(--wm-shadow);
  z-index: 100000;
}
.items {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 6px;
}
.item {
  display: grid;
  place-items: center;
  width: 72px;
  height: 72px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: var(--wm-radius);
  background: transparent;
}
.item.selected {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.item.minimized {
  opacity: 0.6;
}
.title {
  max-width: 480px;
  margin: 10px auto 0;
  overflow: hidden;
  text-align: center;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-weight: bold;
}
</style>
