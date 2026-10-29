import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { soundApi, type SoundChange } from '@/api/sound'
import type { SoundState } from '@/api/types'

/**
 * The host's sound for the panel's applet (D79), kept in sync over the
 * events socket. Volume changes from a slider are sent at most every
 * 100 ms, the last one always.
 */
export const useSound = defineStore('sound', () => {
  const state = ref<SoundState>({ available: false, outputs: [], apps: [] })
  const output = computed(() => state.value.outputs.find((o) => o.default) ?? state.value.outputs[0])
  let seq = 0

  async function refresh() {
    // Mid-drag, the server's state is behind the slider: the sends' own
    // answers bring it.
    if (sending) return
    const n = ++seq
    try {
      const s = await soundApi.state()
      if (n === seq) state.value = s
    } catch {
      // The next event brings it.
    }
  }

  // Changes waiting to be sent, in order; a drag's volumes merge into one.
  const queue: SoundChange[] = []
  let sending = false
  async function flush() {
    if (sending) return
    sending = true
    try {
      while (queue.length) {
        const c = queue.shift()!
        try {
          const s = await soundApi.set(c)
          ++seq
          if (!queue.length) state.value = s
        } catch {
          void refresh()
        }
        await new Promise((r) => setTimeout(r, 100))
      }
    } finally {
      sending = false
    }
  }

  /** Applies a change at once in the applet, and sends it. */
  function set(change: SoundChange) {
    const s = state.value
    if ('output' in change) {
      state.value = {
        ...s,
        outputs: s.outputs.map((o) => ({
          ...o,
          volume: o.name === change.output ? (change.volume ?? o.volume) : o.volume,
          muted: o.name === change.output ? (change.muted ?? o.muted) : o.muted,
          default: change.default ? o.name === change.output : o.default,
        })),
      }
    } else {
      state.value = {
        ...s,
        apps: s.apps.map((a) =>
          a.id === change.app ? { ...a, volume: change.volume ?? a.volume, muted: change.muted ?? a.muted } : a,
        ),
      }
    }
    const last = queue[queue.length - 1]
    if (last && sameTarget(last, change)) queue[queue.length - 1] = { ...last, ...change } as SoundChange
    else queue.push(change)
    void flush()
  }

  function reset() {
    seq++
    state.value = { available: false, outputs: [], apps: [] }
  }

  return { state, output, refresh, set, reset }
})

function sameTarget(a: SoundChange, b: SoundChange): boolean {
  return ('output' in a && 'output' in b && a.output === b.output) || ('app' in a && 'app' in b && a.app === b.app)
}
