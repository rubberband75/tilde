import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface ConfirmOptions {
  title: string
  message?: string
  /** Label of the confirming button, e.g. "Delete". */
  confirm?: string
  /** Show the confirming button as destructive. */
  danger?: boolean
}

interface Pending extends ConfirmOptions {
  id: number
  resolve: (ok: boolean) => void
}

/** Modal confirmation dialogs (ctx.confirm), one at a time, in order. */
export const useDialogs = defineStore('dialogs', () => {
  const queue = ref<Pending[]>([])
  let next = 1

  function confirm(opts: ConfirmOptions): Promise<boolean> {
    return new Promise((resolve) => {
      queue.value = [...queue.value, { ...opts, id: next++, resolve }]
    })
  }

  function answer(id: number, ok: boolean) {
    const d = queue.value.find((p) => p.id === id)
    if (!d) return
    queue.value = queue.value.filter((p) => p.id !== id)
    d.resolve(ok)
  }

  /** Answers every open dialog with "no" (logout). */
  function reset() {
    for (const d of queue.value) d.resolve(false)
    queue.value = []
  }

  return { queue, confirm, answer, reset }
})
