<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const now = ref(new Date())
let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  // Tick on the minute boundary, then every minute.
  const sync = () => {
    now.value = new Date()
  }
  setTimeout(() => {
    sync()
    timer = setInterval(sync, 60_000)
  }, 60_000 - (Date.now() % 60_000))
})
onBeforeUnmount(() => clearInterval(timer))

const time = computed(() => new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(now.value))
const date = computed(() => new Intl.DateTimeFormat(undefined, { dateStyle: 'full' }).format(now.value))
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
  color: #fff;
}
</style>
