import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { networkApi } from '@/api/network'
import type { NetDevice, NetworkState } from '@/api/types'
import { onNetworkChanged } from '@/lib/bus'

/**
 * Which device the browser most likely reaches Tilde through, to warn
 * before it is turned off (D55): the one holding the address in the URL,
 * when the URL has one; otherwise whatever carries the default route
 * (certain: false). null for a browser on the host itself.
 */
export function browserDevice(devices: NetDevice[], hostname: string): { id: string; certain: boolean } | null {
  const host = hostname.replace(/^\[|\]$/g, '').toLowerCase()
  if (host === 'localhost' || host.endsWith('.localhost') || host === '::1' || /^127\./.test(host)) return null
  const literal = /^[\d.]+$/.test(host) || host.includes(':')
  if (literal) {
    const d = devices.find((d) => d.addresses?.some((a) => a.toLowerCase() === host))
    if (d) return { id: d.id, certain: true }
  }
  const d = devices.find((d) => d.default && d.state === 'connected')
  return d ? { id: d.id, certain: false } : null
}

/** Signal bars (0–3) for a strength, as nm-applet draws them. */
export function signalLevel(strength: number): 0 | 1 | 2 | 3 {
  if (strength > 80) return 3
  if (strength > 55) return 2
  if (strength > 30) return 1
  return 0
}

/** The panel applet's picture: what the host is connected through. */
export type NetworkIcon =
  { kind: 'wired' } | { kind: 'wifi'; level: 0 | 1 | 2 | 3 } | { kind: 'connecting' } | { kind: 'offline' }

/** The host's network, from NetworkManager through the worker (D55). */
export const useNetwork = defineStore('network', () => {
  const state = ref<NetworkState | null>(null)
  let off: (() => void) | undefined
  let loading: Promise<void> | null = null
  let again = false

  /** Reloads; calls while one is running make one more afterwards. */
  async function refresh(): Promise<void> {
    if (loading) {
      again = true
      return loading
    }
    loading = (async () => {
      do {
        again = false
        try {
          state.value = await networkApi.state()
        } catch {
          // Kept as it was; the next event tries again.
        }
      } while (again)
    })()
    try {
      await loading
    } finally {
      loading = null
    }
  }

  function start() {
    off ??= onNetworkChanged(() => void refresh())
    void refresh()
  }

  function stop() {
    off?.()
    off = undefined
    state.value = null
  }

  const devices = computed(() => state.value?.devices ?? [])
  const visible = computed(() => !!state.value?.available && devices.value.length > 0)

  const icon = computed<NetworkIcon>(() => {
    const up = devices.value.filter((d) => d.state === 'connected')
    const main = up.find((d) => d.default) ?? up.find((d) => d.type === 'ethernet') ?? up[0]
    if (main?.type === 'ethernet') return { kind: 'wired' }
    if (main) {
      const net = main.networks?.find((n) => n.active)
      return { kind: 'wifi', level: signalLevel(net?.strength ?? 0) }
    }
    if (devices.value.some((d) => d.state === 'connecting')) return { kind: 'connecting' }
    return { kind: 'offline' }
  })

  return { state, devices, visible, icon, refresh, start, stop }
})
