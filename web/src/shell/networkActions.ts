// What the network applet's buttons do (D55): connect, with a password
// dialog for secured Wi-Fi networks and another try when it was wrong;
// disconnect and Wi-Fi off, asking first when the browser may be using
// that connection. polkit's own password dialog (D44) comes up by itself
// while a request waits.
import { ApiError } from '@/api/client'
import { networkApi } from '@/api/network'
import type { NetDevice, WifiNetwork } from '@/api/types'
import type { ConfirmOptions, PromptOptions } from '@/stores/dialogs'
import { browserDevice } from '@/stores/network'

export interface NetworkHost {
  confirm: (opts: ConfirmOptions) => Promise<boolean>
  prompt: (opts: PromptOptions) => Promise<string | null>
  notify: (message: string, kind?: 'info' | 'error') => void
  /** location.hostname: how the browser reaches Tilde. */
  hostname: string
}

function failure(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Connects a wired device, or a Wi-Fi device to a network. */
export async function connect(host: NetworkHost, dev: NetDevice, net?: WifiNetwork): Promise<void> {
  const name = net?.ssid ?? dev.interface
  if (net && (net.security === 'enterprise' || net.security === 'other')) {
    host.notify(`“${name}” uses security that Tilde cannot set up yet.`, 'error')
    return
  }
  let password: string | undefined
  for (;;) {
    let asked: 'new' | 'wrong' | null = null
    try {
      const res = await networkApi.connect(dev.id, net?.ssid, password)
      if (res.connected) return
      if (res.reason === 'wrong_password' && net && net.security !== 'none') asked = 'wrong'
      else {
        const why = res.reason === 'timeout' ? 'it took too long' : 'the connection failed'
        host.notify(`Could not connect to “${name}”: ${why}.`, 'error')
        return
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === 'password_required') asked = 'new'
      else {
        host.notify(`Could not connect to “${name}”: ${failure(e)}`, 'error')
        return
      }
    }
    const answer = await host.prompt({
      title: `Connect to “${name}”`,
      message:
        asked === 'wrong' ? 'The password was not accepted. Type it again.' : 'This Wi-Fi network needs a password.',
      label: 'Password',
      password: true,
      confirm: 'Connect',
    })
    if (answer === null || answer === '') return
    password = answer
  }
}

/**
 * Asks before taking down what the browser may be using: then Tilde stops
 * answering until someone at the machine (or on another network) turns it
 * back on.
 */
async function confirmCutOff(host: NetworkHost, devices: NetDevice[], affected: NetDevice[], what: string) {
  const via = browserDevice(devices, host.hostname)
  if (!via || !affected.some((d) => d.id === via.id)) return true
  return host.confirm({
    title: `${what}?`,
    message: via.certain
      ? 'This browser reaches Tilde through this connection. Without it, Tilde will stop responding here until the connection is back.'
      : 'This browser may reach Tilde through this connection. If it does, Tilde will stop responding here until the connection is back.',
    confirm: what,
    danger: true,
  })
}

export async function disconnect(host: NetworkHost, devices: NetDevice[], dev: NetDevice): Promise<void> {
  if (!(await confirmCutOff(host, devices, [dev], 'Disconnect'))) return
  try {
    await networkApi.disconnect(dev.id)
  } catch (e) {
    host.notify(`Could not disconnect ${dev.interface}: ${failure(e)}`, 'error')
  }
}

export async function setWifi(host: NetworkHost, devices: NetDevice[], enabled: boolean): Promise<void> {
  if (!enabled) {
    const wifi = devices.filter((d) => d.type === 'wifi')
    if (!(await confirmCutOff(host, devices, wifi, 'Turn Off Wi-Fi'))) return
  }
  try {
    await networkApi.setWifi(enabled)
  } catch (e) {
    host.notify(`Could not turn Wi-Fi ${enabled ? 'on' : 'off'}: ${failure(e)}`, 'error')
  }
}
