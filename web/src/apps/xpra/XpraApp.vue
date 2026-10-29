<script setup lang="ts">
// The host's apps, in the user's Xpra session (D26, D78): Xpra's own HTML5
// client in a frame on Tilde's Xpra port (an origin of its own, shared with
// no local web app, as Xpra can run anything as the user), logged in by a
// one-time handoff as local web apps are (D76). The host's windows float
// inside it. Opened by the menu when it launches a host app.
import { onMounted, ref } from 'vue'
import { enterApp } from '@/api/apps'
import { hostAppsApi } from '@/api/hostApps'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx
const frame = `tilde-xpra-${ctx.windowId}`
const error = ref('')

// Xpra's client options: connect at once, no floating menu (Tilde's menu
// launches apps), no sound yet (D55), the clipboard on, and take the
// session over from a browser that had it.
const options = new URLSearchParams({
  submit: 'true',
  floating_menu: 'false',
  sound: 'false',
  clipboard: 'true',
  printing: 'false',
  file_transfer: 'false',
  reconnect: 'true',
  steal: 'true',
  sharing: 'false',
  exit_with_client: 'false',
  exit_with_children: 'false',
})

async function connect() {
  try {
    // Only the desktop starts the session: connecting to it does not.
    await hostAppsApi.session()
    await enterApp(frame, `/xpra/index.html?${options}`, 'xpra')
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(connect)
</script>

<template>
  <div class="xpra">
    <div v-if="error" class="error">
      <p>{{ error }}</p>
      <button class="btn" @click="connect">
        <SymbolicIcon name="refresh" :size="16" />
        Try Again
      </button>
    </div>
    <iframe
      v-else
      :name="frame"
      title="Host apps"
      src="about:blank"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"
      allow="clipboard-write; fullscreen"
      referrerpolicy="no-referrer"
    />
  </div>
</template>

<style scoped>
.xpra {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--view-bg);
  color: var(--window-fg);
}
iframe {
  flex: 1;
  width: 100%;
  border: none;
}
.error {
  margin: 16px;
}
.error p {
  color: var(--danger);
}
</style>
