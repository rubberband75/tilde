<script setup lang="ts">
// A file's icon: its thumbnail when the worker can make one (Entry.thumb,
// freedesktop thumbnail cache), the theme icon otherwise and while the
// thumbnail loads. The box keeps the icon's size, so layouts never shift.
import { computed, ref, watch } from 'vue'
import { fsApi } from '@/api/fs'
import type { Entry } from '@/api/types'
import ThemeIcon from './ThemeIcon.vue'

const props = defineProps<{ name: string[]; size: number; entry?: Entry }>()

const loaded = ref(false)
const failed = ref(false)

const src = computed(() => {
  const e = props.entry
  if (!e?.thumb || failed.value || props.size < 24) return null
  // The spec's sizes: normal is 128 px, large 256 px (for HiDPI and big zoom).
  const large = props.size * (window.devicePixelRatio || 1) > 128
  return fsApi.thumbnailUrl(e.path, e.mtime, large ? 'large' : 'normal')
})

watch(src, () => {
  loaded.value = false
})
</script>

<template>
  <span class="file-icon" :style="{ width: `${size}px`, height: `${size}px` }">
    <ThemeIcon v-if="!loaded" :name="name" :size="size" />
    <img
      v-if="src"
      v-show="loaded"
      class="thumb"
      :src="src"
      alt=""
      draggable="false"
      decoding="async"
      :style="{ maxWidth: `${size}px`, maxHeight: `${size}px` }"
      @load="loaded = true"
      @error="failed = true"
    />
  </span>
</template>

<style scoped>
.file-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
}
.thumb {
  object-fit: contain;
  box-shadow: var(--thumb-shadow);
}
</style>
