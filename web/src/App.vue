<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { api } from '@/api/client'
import type { LockTiming } from '@/api/types'
import Desktop from '@/shell/Desktop.vue'
import LockScreen from '@/shell/LockScreen.vue'
import LoginScreen from '@/shell/LoginScreen.vue'
import { loadIconIndex } from '@/icons/theme'
import { createReporter, trackInput } from '@/lib/activity'
import { useBackground } from '@/stores/background'
import { useClipboard } from '@/stores/clipboard'
import { useDialogs } from '@/stores/dialogs'
import { useFileChooser } from '@/stores/fileChooser'
import { useJobs } from '@/stores/jobs'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'
import { currentStyle } from '@/theme/desktopStyle'

const session = useSession()
const wm = useWindows()
const jobs = useJobs()
const dialogs = useDialogs()
const chooser = useFileChooser()
const clipboard = useClipboard()
const background = useBackground()

document.documentElement.dataset.theme = currentStyle.theme

let stopInput: (() => void) | undefined

// Locking clears the page like logging out does (D40): the window layout
// is saved per user and comes back on unlock, and terminals replay their
// output when they reattach.
watch(
  () => session.status,
  (status) => {
    stopInput?.()
    stopInput = undefined
    if (status === 'loggedIn' && session.info) {
      wm.load(session.info.user)
      jobs.connect()
      const reporter = createReporter(() => api.post<LockTiming>('/api/auth/activity'), session.info.heartbeat)
      stopInput = trackInput(reporter)
    } else {
      wm.reset()
      jobs.disconnect()
      dialogs.reset()
      chooser.reset()
      clipboard.clear()
      background.clear()
    }
  },
)

// A tab coming back may have been locked meanwhile, or unlocked from
// another tab of this browser.
function refresh() {
  if (document.visibilityState === 'visible') void session.refresh()
}

onMounted(() => {
  void loadIconIndex()
  void session.check()
  document.addEventListener('visibilitychange', refresh)
  window.addEventListener('focus', refresh)
})
</script>

<template>
  <div v-if="session.status === 'checking'" class="checking" />
  <Desktop v-else-if="session.status === 'loggedIn'" />
  <LockScreen v-else-if="session.status === 'locked'" />
  <LoginScreen v-else />
</template>

<style scoped>
.checking {
  position: fixed;
  inset: 0;
  background: var(--desktop-bg);
}
</style>
