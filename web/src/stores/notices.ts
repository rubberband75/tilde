import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface Notice {
  id: number
  message: string
  kind: 'info' | 'error'
}

/** Small transient notifications, shown above the panel. */
export const useNotices = defineStore('notices', () => {
  const items = ref<Notice[]>([])
  let next = 1

  function push(message: string, kind: Notice['kind'] = 'info', ms = 5000) {
    const id = next++
    items.value.push({ id, message, kind })
    setTimeout(() => dismiss(id), ms)
  }

  function dismiss(id: number) {
    items.value = items.value.filter((n) => n.id !== id)
  }

  return { items, push, dismiss }
})
