import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { Resources } from '@/api/types'

/** A minute of snapshots, one a second, as the worker keeps (D61). */
export const HISTORY = 60

/** The latest resource snapshots, from the events socket. */
export const useResources = defineStore('resources', () => {
  const history = ref<Resources[]>([])
  const latest = computed(() => history.value.at(-1) ?? null)

  /** The worker's minute so far, when watching starts. */
  function setHistory(list: Resources[]) {
    history.value = list.slice(-HISTORY)
  }

  function add(r: Resources) {
    const next = [...history.value, r]
    history.value = next.length > HISTORY ? next.slice(-HISTORY) : next
  }

  function reset() {
    history.value = []
  }

  return { history, latest, setHistory, add, reset }
})
