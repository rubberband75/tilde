import { createPinia, setActivePinia } from 'pinia'
import { createApp, h, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useShell } from '@/stores/shell'
import { useWindows } from '@/stores/windows'
import StartMenu from './StartMenu.vue'

let host: HTMLElement
let unmount: () => void

beforeEach(() => {
  const pinia = createPinia()
  setActivePinia(pinia)
  host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ render: () => h(StartMenu) }).use(pinia)
  app.mount(host)
  unmount = () => app.unmount()
})

afterEach(() => {
  unmount()
  host.remove()
})

describe('start menu', () => {
  it('closes when the search launches an app', async () => {
    const shell = useShell()
    shell.menuOpen = true
    await nextTick()
    await nextTick()
    const search = document.querySelector<HTMLInputElement>('input[aria-label="Search applications"]')!
    search.value = 'System Monitor'
    search.dispatchEvent(new Event('input'))
    await nextTick()
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    search.dispatchEvent(enter)
    // Otherwise the browser's keypress reaches the menu button, which has
    // the focus by then, and opens the menu again.
    expect(enter.defaultPrevented).toBe(true)
    await nextTick()
    expect(useWindows().windows.map((w) => w.appId)).toEqual(['system-monitor'])
    expect(shell.menuOpen).toBe(false)
    await nextTick()
    await nextTick()
    expect(document.querySelector('input[aria-label="Search applications"]')).toBeNull()
    // The new window keeps the focus, not the menu button.
    expect(document.activeElement?.classList.contains('menu-button')).toBe(false)
  })
})
