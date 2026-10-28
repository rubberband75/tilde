<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useBackground } from '@/stores/background'
import DesktopIcons from './DesktopIcons.vue'
import Dialogs from './Dialogs.vue'
import JobsPanel from './JobsPanel.vue'
import Notices from './Notices.vue'
import Panel from './Panel.vue'
import PolkitDialog from './PolkitDialog.vue'
import WindowSwitcher from './WindowSwitcher.vue'
import WindowLayer from '@/wm/WindowLayer.vue'
import { useWindows } from '@/stores/windows'
import { installShortcuts } from './keyboard'

const wm = useWindows()
const background = useBackground()
const workArea = ref<HTMLElement | null>(null)
let ro: ResizeObserver | undefined
let removeShortcuts: (() => void) | undefined

onMounted(() => {
  void background.load()
  ro = new ResizeObserver(([entry]) => {
    if (entry) wm.setArea(entry.contentRect.width, entry.contentRect.height)
  })
  if (workArea.value) ro.observe(workArea.value)
  // Captured before apps see them, so they work from a terminal too.
  removeShortcuts = installShortcuts()
})
onBeforeUnmount(() => {
  ro?.disconnect()
  removeShortcuts?.()
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
    <WindowSwitcher />
    <Dialogs />
    <PolkitDialog />
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
