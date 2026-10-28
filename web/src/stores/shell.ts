import { defineStore } from 'pinia'
import { ref } from 'vue'

/** Shell state that more than one part of the desktop changes: the start menu, opened by its button and by keys (D53). */
export const useShell = defineStore('shell', () => {
  const menuOpen = ref(false)
  return { menuOpen }
})
