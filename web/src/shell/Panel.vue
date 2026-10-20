<script setup lang="ts">
// The bottom panel: menu, launchers, window list, and the tray on the right.
import { computed } from 'vue'
import { getApp } from '@/apps/registry'
import type { AppManifest } from '@/apps/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import StartMenu from './StartMenu.vue'
import WindowList from './WindowList.vue'
import PanelClock from './PanelClock.vue'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'

const wm = useWindows()
const session = useSession()

const launchers = computed(() => ['files', 'terminal'].map(getApp).filter((a): a is AppManifest => !!a))
</script>

<template>
  <nav class="panel" aria-label="Panel">
    <StartMenu />
    <div class="launchers">
      <button v-for="a in launchers" :key="a.id" class="launcher" :title="a.name" @click="wm.open(a.id)">
        <ThemeIcon :name="a.icon" :size="24" />
      </button>
    </div>
    <WindowList />
    <div class="tray">
      <div class="applet user" :title="`Logged in as ${session.info?.user}`">{{ session.info?.user }}</div>
      <PanelClock />
    </div>
  </nav>
</template>

<style scoped>
.panel {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: var(--panel-height);
  display: flex;
  align-items: stretch;
  background: var(--panel-bg);
  color: var(--panel-fg);
  z-index: 50000;
  box-shadow: 0 -1px 0 rgba(0, 0, 0, 0.4);
}
.launchers {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 7px 0 4px;
}
.launcher {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
}
.launcher:hover {
  background: var(--panel-item-hover-bg);
}
.tray {
  display: flex;
  align-items: stretch;
  padding-right: 2px;
}
.applet.user {
  display: flex;
  align-items: center;
  padding: 0 10px;
  color: var(--panel-item-fg);
}
</style>
