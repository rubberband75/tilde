<script setup lang="ts">
// Toasts, top right: programs' notifications (D70), then Tilde's own
// messages.
import { useNotices } from '@/stores/notices'
import { useNotifications } from '@/stores/notifications'
import NotificationToast from './NotificationToast.vue'

const notices = useNotices()
const notifications = useNotifications()
</script>

<template>
  <div class="notices" aria-live="polite">
    <TransitionGroup name="notice">
      <NotificationToast v-for="n in notifications.toasts" :key="`n${n.id}`" :n="n" />
      <div v-for="n in notices.items" :key="n.id" class="notice" :class="n.kind" @click="notices.dismiss(n.id)">
        {{ n.message }}
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.notices {
  position: absolute;
  right: 12px;
  top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 340px;
  z-index: 60000;
  pointer-events: none;
}
.notice {
  padding: 12px 14px;
  border-radius: var(--menu-radius);
  background: var(--menu-bg);
  color: var(--menu-fg);
  box-shadow: var(--menu-shadow);
  pointer-events: auto;
  border-left: 4px solid var(--accent);
}
.notice.error {
  border-left-color: var(--danger);
}
.notice-enter-active,
.notice-leave-active {
  transition: all 200ms ease;
}
.notice-enter-from,
.notice-leave-to {
  opacity: 0;
  transform: translateX(20px);
}
</style>
