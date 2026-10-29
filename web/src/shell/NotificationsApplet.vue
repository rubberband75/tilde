<script setup lang="ts">
// The panel's notifications applet, as Cinnamon's: a bell with how many
// notifications there are, and the list of them, newest first. Shown
// while Tilde is the notification server (D70), or while any are listed.
import { computed, ref } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import type { Notification } from '@/api/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { ago } from '@/lib/agent'
import { useNotifications } from '@/stores/notifications'
import NotificationIcon from './NotificationIcon.vue'

const store = useNotifications()
const open = ref(false)
const count = computed(() => store.items.length)
const label = computed(() =>
  count.value ? `${count.value} notification${count.value === 1 ? '' : 's'}` : 'No notifications',
)
const when = (n: Notification) => ago(new Date(n.time).toISOString())

function activate(n: Notification) {
  if (n.open && n.actions?.some((a) => a.key === 'default')) void store.invoke(n.id, 'default')
}
</script>

<template>
  <PopoverRoot v-if="store.active || count" v-model:open="open">
    <PopoverTrigger
      class="applet notifications"
      :class="{ active: open }"
      :title="label"
      :aria-label="`Notifications: ${label}`"
    >
      <SymbolicIcon name="notifications" :size="16" />
      <span v-if="count" class="count">{{ count }}</span>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="notifications-menu" side="top" align="end" :side-offset="0">
        <div class="head">
          <strong>Notifications</strong>
          <button class="btn" :disabled="!count" @click="store.clear()">Clear</button>
        </div>
        <p v-if="!count" class="note">No notifications</p>
        <ul v-else class="list" aria-label="Notifications">
          <li v-for="n in store.items" :key="n.id" :class="{ critical: n.urgency === 2 }" @click="activate(n)">
            <NotificationIcon :n="n" :size="32" />
            <div class="what">
              <div class="line">
                <strong class="summary">{{ n.summary }}</strong>
                <time class="time">{{ when(n) }}</time>
              </div>
              <span v-if="n.body" class="body">{{ n.body }}</span>
              <span v-if="n.app" class="app">{{ n.app }}</span>
            </div>
            <button
              class="remove"
              :title="`Remove ${n.summary}`"
              :aria-label="`Remove ${n.summary}`"
              @click.stop="store.clear(n.id)"
            >
              <SymbolicIcon name="close" :size="12" />
            </button>
          </li>
        </ul>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.applet.notifications {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 100%;
  padding: 0 8px;
  border: none;
  background: transparent;
  color: var(--panel-item-fg);
}
.applet.notifications:hover,
.applet.notifications.active {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
.count {
  font-size: 0.85em;
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.notifications-menu {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 360px;
  max-height: calc(100vh - var(--panel-height) - 10px);
  padding: 10px;
  background: var(--menu-bg);
  color: var(--menu-fg);
  border-radius: var(--menu-radius) var(--menu-radius) 0 0;
  box-shadow: var(--menu-shadow);
  z-index: 100000;
  outline: none;
}
.notifications-menu .head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 4px;
}
.notifications-menu .note {
  margin: 4px;
  color: var(--menu-dim-fg);
}
.notifications-menu .list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 4px;
  overflow: auto;
  list-style: none;
  border-radius: var(--menu-radius);
  background: var(--menu-box-bg);
}
.notifications-menu li {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 6px 8px;
  border-radius: var(--menu-radius);
}
.notifications-menu li.critical {
  box-shadow: inset 3px 0 var(--danger);
}
.notifications-menu .what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.notifications-menu .line {
  display: flex;
  gap: 8px;
}
.notifications-menu .summary {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
.notifications-menu .time,
.notifications-menu .body,
.notifications-menu .app {
  color: var(--menu-dim-fg);
}
.notifications-menu .time {
  flex: none;
  font-size: 0.85em;
}
.notifications-menu .body {
  white-space: pre-line;
  overflow-wrap: anywhere;
}
.notifications-menu .app {
  font-size: 0.85em;
}
.notifications-menu .remove {
  display: grid;
  place-items: center;
  flex: none;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: var(--radius);
  background: transparent;
  color: inherit;
}
.notifications-menu .remove:hover {
  background: var(--menu-bg);
}
</style>
