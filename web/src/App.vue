<script setup lang="ts">
import { onMounted, watch } from 'vue'
import Desktop from '@/shell/Desktop.vue'
import LoginScreen from '@/shell/LoginScreen.vue'
import { loadIconIndex } from '@/icons/theme'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'
import { currentStyle } from '@/theme/desktopStyle'

const session = useSession()
const wm = useWindows()

document.documentElement.dataset.theme = currentStyle.theme

watch(
  () => session.status,
  (status) => {
    if (status === 'loggedIn' && session.info) wm.load(session.info.user)
    else wm.reset()
  },
)

onMounted(() => {
  void loadIconIndex()
  void session.check()
})
</script>

<template>
  <div v-if="session.status === 'checking'" class="checking" />
  <Desktop v-else-if="session.status === 'loggedIn'" />
  <LoginScreen v-else />
</template>

<style scoped>
.checking {
  position: fixed;
  inset: 0;
  background: var(--desktop-bg);
}
</style>
