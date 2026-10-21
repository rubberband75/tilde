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

export interface PromptOptions extends ConfirmOptions {
  /** Initial text of the entry. */
  value?: string
  /** Accessible label of the entry. */
  label?: string
}

interface Pending extends PromptOptions {
  id: number
  /** Set for prompts: the dialog shows an entry. */
  input?: string
  resolve: (result: { ok: boolean; value: string }) => void
}

/** Modal dialogs (ctx.confirm, ctx.prompt), one at a time, in order. */
export const useDialogs = defineStore('dialogs', () => {
  const queue = ref<Pending[]>([])
  let next = 1

  function open(opts: PromptOptions, input: string | undefined) {
    return new Promise<{ ok: boolean; value: string }>((resolve) => {
      queue.value = [...queue.value, { ...opts, input, id: next++, resolve }]
    })
  }

  function confirm(opts: ConfirmOptions): Promise<boolean> {
    return open(opts, undefined).then((r) => r.ok)
  }

  /** Asks for a line of text; resolves null if cancelled. */
  function prompt(opts: PromptOptions): Promise<string | null> {
    return open(opts, opts.value ?? '').then((r) => (r.ok ? r.value : null))
  }

  function answer(id: number, ok: boolean, value = '') {
    const d = queue.value.find((p) => p.id === id)
    if (!d) return
    queue.value = queue.value.filter((p) => p.id !== id)
    d.resolve({ ok, value })
  }

  /** Answers every open dialog with "no" (logout). */
  function reset() {
    for (const d of queue.value) d.resolve({ ok: false, value: '' })
    queue.value = []
  }

  return { queue, confirm, prompt, answer, reset }
})
