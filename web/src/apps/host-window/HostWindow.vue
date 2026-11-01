<script setup lang="ts">
// One of the host's windows (D80, docs/xpra-client.md): the canvas the host
// windows store draws on. Closing asks the app, which may refuse.
import { onMounted, ref } from 'vue'
import type { AppContext } from '@/apps/types'
import { useHostWindows } from '@/stores/hostWindows'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx
const host = useHostWindows()
const wid = Number(ctx.args.wid)
const el = ref<HTMLElement | null>(null)

ctx.setCloseGuard(() => host.requestClose(wid))

onMounted(() => {
  const canvas = host.canvasOf(wid)
  if (canvas) el.value?.append(canvas)
  else ctx.close()
})
</script>

<template>
  <div ref="el" class="host-window" />
</template>

<style scoped>
.host-window {
  height: 100%;
  overflow: hidden;
  background: var(--view-bg);
}
.host-window :deep(canvas) {
  display: block;
}
</style>
