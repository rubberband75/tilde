<script setup lang="ts">
import { useClock } from '@/lib/clock'

const { time, date } = useClock()
</script>

<template>
  <div class="clock applet" :title="date">{{ time }}</div>
</template>

<style scoped>
.clock {
  display: flex;
  align-items: center;
  height: 100%;
  padding: 0 10px;
  color: var(--panel-fg);
  font-weight: bold;
}
.clock:hover {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
</style>
