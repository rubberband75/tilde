import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NetDevice, NetworkState } from '@/api/types'
import { emitNetworkChanged } from '@/lib/bus'
import { browserDevice, signalLevel, useNetwork } from './network'

const state = vi.fn<() => Promise<NetworkState>>()
vi.mock('@/api/network', () => ({ networkApi: { state: () => state() } }))

const eth: NetDevice = {
  id: '/d/1',
  interface: 'enp3s0',
  type: 'ethernet',
  state: 'connected',
  addresses: ['192.168.1.20', '2001:db8::20'],
  default: true,
}
const wifi: NetDevice = {
  id: '/d/2',
  interface: 'wlp2s0',
  type: 'wifi',
  state: 'connected',
  addresses: ['10.0.0.5'],
  default: false,
  networks: [{ ssid: 'HomeNet', strength: 62, security: 'psk', active: true, known: true }],
}

beforeEach(() => {
  setActivePinia(createPinia())
  state.mockReset()
})

describe('browserDevice', () => {
  it('finds the device holding the address in the URL', () => {
    expect(browserDevice([eth, wifi], '10.0.0.5')).toEqual({ id: '/d/2', certain: true })
    expect(browserDevice([eth, wifi], '[2001:DB8::20]')).toEqual({ id: '/d/1', certain: true })
  })

  it('falls back to the default route for names and unknown addresses', () => {
    expect(browserDevice([eth, wifi], 'mybox.local')).toEqual({ id: '/d/1', certain: false })
    expect(browserDevice([eth, wifi], '203.0.113.7')).toEqual({ id: '/d/1', certain: false })
    expect(browserDevice([{ ...eth, state: 'disconnected' }, wifi], 'mybox.local')).toBeNull()
  })

  it('knows a browser on the host itself cannot be cut off', () => {
    for (const h of ['localhost', '127.0.0.1', '[::1]', 'tilde.localhost'])
      expect(browserDevice([eth, wifi], h)).toBeNull()
  })
})

describe('network store', () => {
  it('shows what the host is connected through', async () => {
    const net = useNetwork()
    state.mockResolvedValue({ available: true, wifiEnabled: true, wifiHardware: true, devices: [wifi] })
    await net.refresh()
    expect(net.visible).toBe(true)
    expect(net.icon).toEqual({ kind: 'wifi', level: 2 })

    state.mockResolvedValue({ available: true, wifiEnabled: true, wifiHardware: true, devices: [eth, wifi] })
    await net.refresh()
    expect(net.icon).toEqual({ kind: 'wired' })

    state.mockResolvedValue({
      available: true,
      wifiEnabled: false,
      wifiHardware: true,
      devices: [{ ...eth, state: 'unavailable', default: false }],
    })
    await net.refresh()
    expect(net.icon).toEqual({ kind: 'offline' })
  })

  it('stays hidden without NetworkManager', async () => {
    const net = useNetwork()
    state.mockResolvedValue({ available: false, wifiEnabled: false, wifiHardware: false, devices: [] })
    await net.refresh()
    expect(net.visible).toBe(false)
  })

  it('reloads on the worker’s events, one load at a time', async () => {
    const net = useNetwork()
    let release: () => void = () => {}
    state.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve({ available: true, wifiEnabled: true, wifiHardware: true, devices: [eth] })
        }),
    )
    net.start()
    emitNetworkChanged()
    emitNetworkChanged()
    expect(state).toHaveBeenCalledTimes(1)
    release()
    await vi.waitFor(() => expect(state).toHaveBeenCalledTimes(2))
    release()
    await vi.waitFor(() => expect(net.devices).toHaveLength(1))
    net.stop()
    emitNetworkChanged()
    expect(state).toHaveBeenCalledTimes(2)
  })
})

describe('signalLevel', () => {
  it('draws bars as nm-applet', () => {
    expect([0, 31, 56, 81].map(signalLevel)).toEqual([0, 1, 2, 3])
  })
})
