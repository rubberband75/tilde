import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import type { ConnectResult, NetDevice, WifiNetwork } from '@/api/types'
import { connect, disconnect, setWifi, type NetworkHost } from './networkActions'

const api = vi.hoisted(() => ({
  connect: vi.fn<(device: string, ssid?: string, password?: string) => Promise<ConnectResult>>(),
  disconnect: vi.fn<(device: string) => Promise<void>>(),
  setWifi: vi.fn<(enabled: boolean) => Promise<void>>(),
}))
vi.mock('@/api/network', () => ({ networkApi: api }))

const eth: NetDevice = {
  id: '/d/1',
  interface: 'enp3s0',
  type: 'ethernet',
  state: 'connected',
  addresses: ['192.168.1.20'],
  default: true,
}
const wifi: NetDevice = { id: '/d/2', interface: 'wlp2s0', type: 'wifi', state: 'disconnected', default: false }
const secured: WifiNetwork = { ssid: 'HomeNet', strength: 70, security: 'psk', active: false, known: false }

let host: NetworkHost & { prompts: string[] }
let answers: (string | null)[]

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset()
  answers = []
  host = {
    prompts: [],
    hostname: '192.168.1.20',
    confirm: vi.fn(async () => true),
    prompt: vi.fn(async (o) => {
      host.prompts.push(o.message ?? '')
      return answers.shift() ?? null
    }),
    notify: vi.fn(),
  }
})

describe('connect', () => {
  it('asks for a new network’s password, and again when it was wrong', async () => {
    api.connect
      .mockRejectedValueOnce(new ApiError(400, 'password_required', 'this network needs a password'))
      .mockResolvedValueOnce({ connected: false, reason: 'wrong_password' })
      .mockResolvedValueOnce({ connected: true, reason: '' })
    answers = ['wrong', 'right']
    await connect(host, wifi, secured)
    expect(api.connect.mock.calls).toEqual([
      ['/d/2', 'HomeNet', undefined],
      ['/d/2', 'HomeNet', 'wrong'],
      ['/d/2', 'HomeNet', 'right'],
    ])
    expect(host.prompts).toEqual([
      'This Wi-Fi network needs a password.',
      'The password was not accepted. Type it again.',
    ])
    expect(host.notify).not.toHaveBeenCalled()
  })

  it('stops when the password dialog is cancelled', async () => {
    api.connect.mockRejectedValue(new ApiError(400, 'password_required', 'x'))
    await connect(host, wifi, secured)
    expect(api.connect).toHaveBeenCalledOnce()
  })

  it('says why it failed otherwise', async () => {
    api.connect.mockResolvedValueOnce({ connected: false, reason: 'timeout' })
    await connect(host, eth)
    expect(host.notify).toHaveBeenCalledWith('Could not connect to “enp3s0”: it took too long.', 'error')

    api.connect.mockRejectedValueOnce(new ApiError(403, 'not_authorized', 'not allowed'))
    await connect(host, wifi, { ...secured, known: true })
    expect(host.notify).toHaveBeenLastCalledWith('Could not connect to “HomeNet”: not allowed', 'error')
    expect(host.prompt).not.toHaveBeenCalled()
  })

  it('does not try networks it cannot set up', async () => {
    await connect(host, wifi, { ...secured, security: 'enterprise' })
    expect(api.connect).not.toHaveBeenCalled()
    expect(host.notify).toHaveBeenCalledOnce()
  })
})

describe('cutting off the browser', () => {
  it('asks before disconnecting the connection the browser uses', async () => {
    await disconnect(host, [eth, wifi], eth)
    expect(host.confirm).toHaveBeenCalledOnce()
    expect(api.disconnect).toHaveBeenCalledWith('/d/1')

    vi.mocked(host.confirm).mockResolvedValueOnce(false)
    await disconnect(host, [eth, wifi], eth)
    expect(api.disconnect).toHaveBeenCalledOnce()
  })

  it('does not ask for another connection, or Wi-Fi when the browser is on wired', async () => {
    await disconnect(host, [eth, wifi], wifi)
    await setWifi(host, [eth, wifi], false)
    expect(host.confirm).not.toHaveBeenCalled()
    expect(api.setWifi).toHaveBeenCalledWith(false)
  })

  it('asks before Wi-Fi off when the browser comes over Wi-Fi', async () => {
    const onWifi = { ...wifi, state: 'connected' as const, addresses: ['10.0.0.5'] }
    host.hostname = '10.0.0.5'
    await setWifi(host, [eth, onWifi], false)
    expect(host.confirm).toHaveBeenCalledOnce()
    await setWifi(host, [eth, onWifi], true)
    expect(host.confirm).toHaveBeenCalledOnce()
  })
})
