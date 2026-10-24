<script setup lang="ts">
// Monochrome UI glyphs (toolbar arrows, window buttons) drawn with
// currentColor, the role symbolic icons play in GTK.
import { computed } from 'vue'

export type SymbolicName =
  | 'back'
  | 'forward'
  | 'up'
  | 'home'
  | 'minimize'
  | 'maximize'
  | 'restore'
  | 'close'
  | 'grid'
  | 'list'
  | 'search'
  | 'refresh'
  | 'logout'
  | 'lock'
  | 'chevron-right'
  | 'chevron-up'
  | 'chevron-down'
  | 'zoom-in'
  | 'zoom-out'
  | 'eject'
  | 'devices'
  | 'edit'
  | 'folder-new'
  | 'rotate-left'
  | 'rotate-right'
  | 'fullscreen'
  | 'trash'
  | 'play'

const props = withDefaults(defineProps<{ name: SymbolicName; size?: number }>(), { size: 16 })

const paths: Record<SymbolicName, string> = {
  back: 'M10 3 5 8l5 5',
  forward: 'M6 3l5 5-5 5',
  up: 'M3 10l5-5 5 5',
  home: 'M2.5 8 8 3l5.5 5M4 7v6h3v-3h2v3h3V7',
  minimize: 'M4 11.5h8',
  maximize: 'M4 4h8v8H4z',
  restore: 'M5.5 3.5h7v7M3.5 5.5h7v7h-7z',
  close: 'M4.5 4.5l7 7M11.5 4.5l-7 7',
  grid: 'M2.5 2.5h4v4h-4zM9.5 2.5h4v4h-4zM2.5 9.5h4v4h-4zM9.5 9.5h4v4h-4z',
  list: 'M2 3.5h12M2 8h12M2 12.5h12',
  search: 'M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.5 10.5 14 14',
  refresh: 'M13 8a5 5 0 1 1-1.5-3.5M13 2.5v3h-3',
  logout: 'M6 2.5H3v11h3M10 5l3 3-3 3M13 8H6',
  lock: 'M4 7.5h8v6H4zM5.5 7.5V5a2.5 2.5 0 0 1 5 0v2.5',
  'chevron-right': 'M6 4l4 4-4 4',
  'chevron-up': 'M4 10l4-4 4 4',
  'chevron-down': 'M4 6l4 4 4-4',
  'zoom-in': 'M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.5 10.5 14 14M5 7h4M7 5v4',
  'zoom-out': 'M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.5 10.5 14 14M5 7h4',
  eject: 'M8 3.5 3.5 9h9zM3.5 12.5h9',
  devices: 'M1.5 2.5h9v7h-9zM3.5 12.5h5M6 9.5v3M11.5 6.5h3v7h-3z',
  edit: 'M10.5 2.5l3 3-8 8h-3v-3zM8.5 4.5l3 3',
  'folder-new': 'M1.5 3.5h4l1.5 1.5h7.5v8h-13zM8 7v4M6 9h4',
  'rotate-left': 'M3.5 8a4.5 4.5 0 1 0 1.3-3.2M4.5 2v3h3',
  'rotate-right': 'M12.5 8a4.5 4.5 0 1 1-1.3-3.2M11.5 2v3h-3',
  fullscreen: 'M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10',
  trash: 'M2.5 4.5h11M6 4.5V2.5h4v2M4 4.5l.8 9h6.4l.8-9M6.8 7v4M9.2 7v4',
  play: 'M5 3l8 5-8 5z',
}

const d = computed(() => paths[props.name])
</script>

<template>
  <svg
    class="symbolic"
    :width="size"
    :height="size"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="d" />
  </svg>
</template>

<style scoped>
.symbolic {
  display: block;
  flex: none;
}
</style>
