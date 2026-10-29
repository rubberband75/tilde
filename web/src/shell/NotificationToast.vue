<script setup lang="ts">
// A program's notification as a toast (D70), as Cinnamon shows them: its
// picture, summary and body, and its buttons. A click on it is its
// "default" action, or dismisses it if it has none.
import { computed } from 'vue'
import type { Notification } from '@/api/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { useNotifications } from '@/stores/notifications'
import NotificationIcon from './NotificationIcon.vue'

const props = defineProps<{ n: Notification }>()
const store = useNotifications()

const buttons = computed(() => (props.n.actions ?? []).filter((a) => a.key !== 'default'))
const hasDefault = computed(() => (props.n.actions ?? []).some((a) => a.key === 'default'))

function click() {
  if (hasDefault.value) void store.invoke(props.n.id, 'default')
  else void store.dismiss(props.n.id)
}
</script>

<template>
  <div
    class="toast"
    :class="{ critical: n.urgency === 2 }"
    role="status"
    :aria-label="`${n.app || 'Notification'}: ${n.summary}`"
    @click="click"
  >
    <NotificationIcon :n="n" :size="48" />
    <div class="text">
      <div class="top">
        <strong class="summary">{{ n.summary }}</strong>
        <button class="close" title="Close" aria-label="Close" @click.stop="store.dismiss(n.id)">
          <SymbolicIcon name="close" :size="14" />
        </button>
      </div>
      <p v-if="n.body" class="body">{{ n.body }}</p>
      <div v-if="buttons.length" class="actions">
        <button v-for="a in buttons" :key="a.key" class="btn" @click.stop="store.invoke(n.id, a.key)">
          {{ a.label }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.toast {
  display: flex;
  gap: 12px;
  padding: 12px 10px 12px 14px;
  border-radius: var(--menu-radius);
  background: var(--menu-bg);
  color: var(--menu-fg);
  box-shadow: var(--menu-shadow);
  border-left: 4px solid var(--accent);
  pointer-events: auto;
  cursor: default;
}
.toast.critical {
  border-left-color: var(--danger);
}
.text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.top {
  display: flex;
  align-items: flex-start;
  gap: 6px;
}
.summary {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
.close {
  display: grid;
  place-items: center;
  flex: none;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: var(--radius);
  background: transparent;
  color: var(--menu-dim-fg);
}
.close:hover {
  background: var(--menu-item-hover-bg);
  color: var(--menu-item-hover-fg);
}
.body {
  display: -webkit-box;
  margin: 0;
  overflow: hidden;
  color: var(--menu-dim-fg);
  white-space: pre-line;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 5;
  line-clamp: 5;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
</style>
