<script setup lang="ts">
// A notification's picture: one the program sent, a picture file of the
// user's, or an icon by name, falling back to the generic one.
import { computed, ref, watch } from 'vue'
import { fsApi } from '@/api/fs'
import { notificationsApi } from '@/api/notifications'
import type { Notification } from '@/api/types'
import ThemeIcon from '@/components/ThemeIcon.vue'

const props = withDefaults(defineProps<{ n: Notification; size?: number }>(), { size: 48 })

const src = computed(() => {
  if (props.n.image) return `${notificationsApi.imageUrl(props.n.id)}&t=${props.n.time}`
  if (props.n.iconPath) return fsApi.contentUrl(props.n.iconPath)
  return ''
})
const broken = ref(false)
watch(src, () => (broken.value = false))

const names = computed(() => [
  ...(props.n.icon ? [props.n.icon] : []),
  props.n.urgency === 2 ? 'dialog-warning' : 'preferences-system-notifications',
])
</script>

<template>
  <img
    v-if="src && !broken"
    class="picture"
    :src="src"
    :width="size"
    :height="size"
    alt=""
    draggable="false"
    @error="broken = true"
  />
  <ThemeIcon v-else :name="names" :size="size" />
</template>

<style scoped>
.picture {
  flex: none;
  object-fit: contain;
  border-radius: var(--radius);
}
</style>
