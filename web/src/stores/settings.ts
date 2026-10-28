import { defineStore } from 'pinia'
import { ref, toRaw } from 'vue'
import { settingsApi } from '@/api/settings'
import type { LockSettings, Settings } from '@/api/types'

/** What applies until the worker answers, and without a settings file. */
export function defaultSettings(): Settings {
  return {
    lock: { own: false, enabled: true, minutes: 15 },
    effects: { animations: true },
    panel: { resources: true, network: true, fullscreen: true },
  }
}

/**
 * Tilde's settings (D72) for the shell: loaded with the desktop, again when
 * any browser saves them (the "settings" event), and saved as a whole.
 */
export const useSettings = defineStore('settings', () => {
  const settings = ref<Settings>(defaultSettings())
  const desktopLock = ref<LockSettings | null>(null)

  async function load() {
    try {
      const res = await settingsApi.get()
      settings.value = res.settings
      desktopLock.value = res.desktopLock
    } catch {
      // Kept as they were; the defaults until the first answer.
    }
  }

  /**
   * Applies a change at once and saves it; a save that fails puts the
   * previous settings back and rejects.
   */
  async function save(change: (s: Settings) => void) {
    const before = settings.value
    const next = structuredClone(toRaw(before))
    change(next)
    settings.value = next
    try {
      settings.value = await settingsApi.save(next)
    } catch (e) {
      settings.value = before
      throw e
    }
  }

  function reset() {
    settings.value = defaultSettings()
    desktopLock.value = null
  }

  return { settings, desktopLock, load, save, reset }
})
