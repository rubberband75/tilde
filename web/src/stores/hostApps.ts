import { defineStore } from 'pinia'
import { ref } from 'vue'
import { hostAppsApi } from '@/api/hostApps'
import type { HostApp } from '@/api/types'

/**
 * The host's graphical apps for the menu (D78), fetched when the menu
 * opens; they run in the user's Xpra session, shown in the Xpra window.
 */
export const useHostApps = defineStore('hostApps', () => {
  const available = ref(false)
  const apps = ref<HostApp[]>([])
  let loading: Promise<void> | null = null

  function load(): Promise<void> {
    loading ??= hostAppsApi
      .list()
      .then((res) => {
        available.value = res.available
        apps.value = res.apps
      })
      .catch(() => {
        // Kept as they were: the menu shows Tilde's apps either way.
      })
      .finally(() => (loading = null))
    return loading
  }

  return { available, apps, load }
})
