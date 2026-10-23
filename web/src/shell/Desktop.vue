<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useBackground } from '@/stores/background'
import DesktopIcons from './DesktopIcons.vue'
import Dialogs from './Dialogs.vue'
import JobsPanel from './JobsPanel.vue'
import Notices from './Notices.vue'
import Panel from './Panel.vue'
import WindowLayer from '@/wm/WindowLayer.vue'
import { useWindows } from '@/stores/windows'
import { lockScreen } from './actions'

const wm = useWindows()
const background = useBackground()
const workArea = ref<HTMLElement | null>(null)
let ro: ResizeObserver | undefined

// Cinnamon's Lock Screen shortcut. Captured before apps see it, so it works
// from a terminal too. Windows reports AltGr as Ctrl+Alt: AltGr+L types "ł".
function onKey(e: KeyboardEvent) {
  if (e.ctrlKey && e.altKey && !e.shiftKey && !e.metaKey && e.code === 'KeyL' && !e.getModifierState('AltGraph')) {
    e.preventDefault()
    e.stopPropagation()
    void lockScreen()
  }
}

onMounted(() => {
  void background.load()
  ro = new ResizeObserver(([entry]) => {
    if (entry) wm.setArea(entry.contentRect.width, entry.contentRect.height)
  })
  if (workArea.value) ro.observe(workArea.value)
  window.addEventListener('keydown', onKey, true)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  window.removeEventListener('keydown', onKey, true)
})
</script>

<template>
  <div class="desktop" :style="background.style">
    <div ref="workArea" class="work-area">
      <DesktopIcons />
      <WindowLayer />
      <Notices />
      <JobsPanel />
    </div>
    <Panel />
    <Dialogs />
  </div>
</template>

<style scoped>
.desktop {
  position: fixed;
  inset: 0;
  background: var(--desktop-bg);
  overflow: hidden;
}
.work-area {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
  bottom: var(--panel-height);
}
</style>
