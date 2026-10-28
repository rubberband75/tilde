<script setup lang="ts">
// Panel window list: one button per window. Clicking the focused window
// minimizes it; clicking any other brings it up.
import ThemeIcon from '@/components/ThemeIcon.vue'
import { useWindows } from '@/stores/windows'

const wm = useWindows()
</script>

<template>
  <div class="window-list" role="toolbar" aria-label="Windows">
    <button
      v-for="w in wm.ordered"
      :key="w.id"
      :data-window="w.id"
      class="item"
      :class="{ focused: wm.focusedId === w.id && w.state !== 'minimized', minimized: w.state === 'minimized' }"
      :title="w.title"
      @click="wm.activate(w.id)"
      @auxclick.middle="wm.requestClose(w.id)"
    >
      <ThemeIcon :name="w.icon" :size="24" />
      <span class="label">{{ w.title }}</span>
    </button>
  </div>
</template>

<style scoped>
.window-list {
  flex: 1;
  min-width: 0;
  display: flex;
  height: 100%;
  overflow: hidden;
}
.item {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 48px;
  max-width: 200px;
  flex: 0 1 200px;
  height: 100%;
  padding: 0 8px;
  border: none;
  border-top: 2px solid transparent;
  background: transparent;
  color: var(--panel-item-fg);
  font-weight: bold;
  transition: background-color 100ms ease;
}
.item:hover {
  background: var(--panel-item-hover-bg);
  color: var(--panel-fg);
}
.item.focused {
  background: var(--panel-item-active-bg);
  border-top-color: var(--panel-item-active-border);
  color: var(--panel-fg);
}
.item.minimized .label {
  opacity: 0.7;
}
.label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
</style>
