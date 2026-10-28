<script setup lang="ts">
import WindowFrame from './WindowFrame.vue'
import { useWindows } from '@/stores/windows'

const wm = useWindows()
</script>

<template>
  <div class="window-layer">
    <!-- Before the windows, so the dragged window (same z) is drawn over it. -->
    <Transition name="snap">
      <div
        v-if="wm.snapPreview"
        class="snap-preview"
        :style="{
          left: `${wm.snapPreview.rect.x}px`,
          top: `${wm.snapPreview.rect.y}px`,
          width: `${wm.snapPreview.rect.w}px`,
          height: `${wm.snapPreview.rect.h}px`,
          zIndex: wm.snapPreview.z,
        }"
      />
    </Transition>
    <WindowFrame v-for="w in wm.windows" :key="w.id" :win="w" />
    <!-- During a drag or resize, cover everything so iframes (future Xpra
         windows, local web apps) cannot swallow pointer events. -->
    <div
      v-if="wm.interacting"
      class="interaction-shield"
      :style="{ cursor: wm.cursor || undefined }"
      @contextmenu.prevent
    />
  </div>
</template>

<style scoped>
/* Windows stack among themselves, below the panel and the notices, however
   high focus changes or Always on Top take their z-index. */
.window-layer {
  position: absolute;
  inset: 0;
  isolation: isolate;
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
.snap-preview {
  position: absolute;
  box-sizing: border-box;
  border: 1px solid var(--wm-snap-preview-border);
  border-radius: var(--wm-radius);
  background: var(--wm-snap-preview-bg);
  transition:
    left 0.12s ease-out,
    top 0.12s ease-out,
    width 0.12s ease-out,
    height 0.12s ease-out;
}
.snap-enter-active,
.snap-leave-active {
  transition: opacity 0.12s ease-out;
}
.snap-enter-from,
.snap-leave-to {
  opacity: 0;
}
@media (prefers-reduced-motion: reduce) {
  .snap-preview,
  .snap-enter-active,
  .snap-leave-active {
    transition: none;
  }
}
</style>
