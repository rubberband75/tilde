import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ClipboardOp = 'copy' | 'cut'

/**
 * The file clipboard shared by Files windows and the desktop (Ctrl+C,
 * Ctrl+X, Ctrl+V). Paths also go to the system clipboard as text, so they
 * can be pasted into a terminal, as with Nemo.
 */
export const useClipboard = defineStore('clipboard', () => {
  const op = ref<ClipboardOp | null>(null)
  const paths = ref<string[]>([])

  function set(next: ClipboardOp, list: string[]) {
    op.value = next
    paths.value = [...list]
    void navigator.clipboard?.writeText(list.join('\n')).catch(() => {})
  }

  function clear() {
    op.value = null
    paths.value = []
  }

  return { op, paths, set, clear }
})
