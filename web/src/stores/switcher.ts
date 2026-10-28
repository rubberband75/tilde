import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useWindows, type AppWindow } from './windows'

/**
 * Alt+Tab (D53): while Alt is held, Tab steps through the windows, most
 * recently used first, and letting go of Alt brings up the chosen one.
 * Minimized windows are in the list, as in Cinnamon; dialogs come up with
 * their parent instead.
 */
export const useSwitcher = defineStore('switcher', () => {
  const wm = useWindows()
  const ids = ref<string[]>([])
  const index = ref(0)
  const active = computed(() => ids.value.length > 0)
  const items = computed(() => ids.value.map((id) => wm.get(id)).filter((w): w is AppWindow => !!w))
  const selectedId = computed(() => ids.value[index.value])

  /** Opens the switcher on the next window (or the last one, going back). */
  function start(back = false) {
    // z counts focus changes, so it orders windows by when they were last used.
    const list = [...wm.ordered].sort((a, b) => b.z - a.z).map((w) => w.id)
    if (list.length === 0) return
    ids.value = list
    const focusedTop = wm.get(wm.focusedId ?? '')?.parentId ?? wm.focusedId
    // The current window is first only if it is on screen; otherwise
    // (all minimized, Show Desktop) the first is the one to go to.
    const current = list[0] === focusedTop && !wm.isHidden(wm.get(list[0]!)!)
    index.value = back ? list.length - 1 : current ? Math.min(1, list.length - 1) : 0
  }

  function step(back = false) {
    const n = ids.value.length
    if (n) index.value = (index.value + (back ? n - 1 : 1)) % n
  }

  function select(id: string) {
    const i = ids.value.indexOf(id)
    if (i >= 0) index.value = i
  }

  function commit() {
    const id = selectedId.value
    cancel()
    if (id) wm.focus(id)
  }

  function cancel() {
    ids.value = []
    index.value = 0
  }

  return { ids, index, active, items, selectedId, start, step, select, commit, cancel }
})
