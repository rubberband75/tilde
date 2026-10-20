<script setup lang="ts">
import WindowFrame from './WindowFrame.vue'
import { useWindows } from '@/stores/windows'

const wm = useWindows()
</script>

<template>
  <div class="window-layer">
    <WindowFrame v-for="w in wm.windows" :key="w.id" :win="w" />
    <!-- During a drag or resize, cover everything so iframes (future Xpra
         windows, local web apps) cannot swallow pointer events. -->
    <div v-if="wm.interacting" class="interaction-shield" />
  </div>
</template>

<style scoped>
.window-layer {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.window-layer > :deep(.window) {
  pointer-events: auto;
}
.interaction-shield {
  position: absolute;
  inset: 0;
  z-index: 2147483000;
  pointer-events: auto;
}
</style>
