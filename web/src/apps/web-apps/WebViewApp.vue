<script setup lang="ts">
// A local web app in a window (D76): a frame on Tilde's app port, another
// origin, logged in by posting a one-time code to it. Launched by Web Apps
// with { port, title?, keepPrefix?, path? }. The frame is sandboxed but for
// moving the Tilde tab itself (a fake lock screen there would ask for the
// password), and may write the clipboard, not read it.
import { computed, onMounted, ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { appPath, enterApp } from '@/api/apps'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx
const args = ctx.args

const port = Number(args.port)
const title = typeof args.title === 'string' ? args.title : ''
const path = typeof args.path === 'string' ? args.path : '/'
/** The app was told its base path (/absproxy/<port>/), so the prefix stays. */
const keepPrefix = ref(args.keepPrefix === true)
const frame = `tilde-app-${ctx.windowId}`
const error = ref('')
const origin = ref('')
const next = computed(() => appPath(port, keepPrefix.value, path))
const address = computed(() => `localhost:${port}${keepPrefix.value ? next.value : path}`)

ctx.setTitle(title ? `${title} (port ${port})` : `Port ${port}`)

async function enter(target = frame) {
  try {
    origin.value = await enterApp(target, next.value)
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(() => {
  if (Number.isInteger(port) && port >= 1 && port <= 65535) void enter()
  else error.value = 'Not a port.'
})

function setKeepPrefix(on: boolean) {
  keepPrefix.value = on
  ctx.saveState({ keepPrefix: on })
  void enter()
}
</script>

<template>
  <div class="web-view">
    <div class="toolbar">
      <button class="btn" title="Reload" aria-label="Reload" @click="enter()">
        <SymbolicIcon name="refresh" :size="16" />
      </button>
      <span class="address" :title="origin ? `Served from ${origin}` : undefined">{{ address }}</span>
      <DropdownMenuRoot>
        <DropdownMenuTrigger class="btn" aria-label="App menu" title="App menu">
          <SymbolicIcon name="chevron-down" :size="16" />
        </DropdownMenuTrigger>
        <DropdownMenuPortal>
          <DropdownMenuContent class="popup-menu" align="end" :side-offset="4">
            <DropdownMenuItem class="item" @select="enter('_blank')">Open in a Browser Tab</DropdownMenuItem>
            <DropdownMenuItem v-if="!keepPrefix" class="item" @select="setKeepPrefix(true)">
              The App Has Its Base Path Set (/absproxy/{{ port }}/)
            </DropdownMenuItem>
            <DropdownMenuItem v-else class="item" @select="setKeepPrefix(false)">
              The App Uses Relative Links (/proxy/{{ port }}/)
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenuPortal>
      </DropdownMenuRoot>
    </div>
    <p v-if="error" class="error">{{ error }}</p>
    <iframe
      v-else
      :name="frame"
      :title="title || `Port ${port}`"
      src="about:blank"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"
      allow="clipboard-write; fullscreen"
      referrerpolicy="no-referrer"
    />
  </div>
</template>

<style scoped>
.web-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
}
.toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}
.address {
  flex: 1;
  min-width: 0;
  padding: 3px 8px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  border-radius: var(--radius);
  background: var(--view-bg);
  color: var(--view-dim-fg);
}
iframe {
  flex: 1;
  width: 100%;
  border: none;
  background: var(--view-bg);
}
.error {
  margin: 16px;
  color: var(--danger);
}
</style>
