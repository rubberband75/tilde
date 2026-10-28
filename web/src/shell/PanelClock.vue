<script setup lang="ts">
// The panel clock; clicking it opens the calendar (D54).
import { ref } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { useClock } from '@/lib/clock'
import CalendarPopup from './CalendarPopup.vue'

const { now, time, date } = useClock()
const open = ref(false)
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger class="clock applet" :class="{ active: open }" :title="date">{{ time }}</PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="calendar-menu" side="top" align="end" :side-offset="0">
        <CalendarPopup :now="now" />
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.clock {
  display: flex;
  align-items: center;
  height: 100%;
  padding: 0 10px;
  border: none;
  background: transparent;
  color: var(--panel-fg);
  font: inherit;
  font-weight: bold;
}
.clock:hover,
.clock.active {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.calendar-menu {
  width: 300px;
  padding: 10px;
  background: var(--menu-bg);
  color: var(--menu-fg);
  border-radius: var(--menu-radius) var(--menu-radius) 0 0;
  box-shadow: var(--menu-shadow);
  z-index: 100000;
  outline: none;
}
</style>
