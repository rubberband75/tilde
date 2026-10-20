<script setup lang="ts">
// An icon from the bundled icon theme, by freedesktop icon name. Pass
// several names to fall back from specific to generic ("text-x-python",
// "text-x-generic").
import { computed, onMounted } from 'vue'
import { fallbackGlyph, iconIndex, loadIconIndex, resolveIcon } from '@/icons/theme'

const props = withDefaults(defineProps<{ name: string | string[]; size?: number }>(), { size: 24 })

const names = computed(() => (Array.isArray(props.name) ? props.name : [props.name]))
const resolved = computed(() => (iconIndex.value ? resolveIcon(names.value, props.size) : null))
const glyph = computed(() => fallbackGlyph(names.value))

onMounted(loadIconIndex)
</script>

<template>
  <img
    v-if="resolved"
    class="theme-icon"
    :src="resolved.src"
    :srcset="resolved.srcset || undefined"
    :width="size"
    :height="size"
    alt=""
    draggable="false"
  />
  <span v-else class="theme-icon glyph" :style="{ width: `${size}px`, height: `${size}px` }" v-html="glyph" />
</template>

<style scoped>
.theme-icon {
  display: inline-block;
  flex: none;
  object-fit: contain;
}
.glyph :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}
</style>
