import { createPinia, setActivePinia } from 'pinia'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppManifest } from '@/apps/types'
import { useWindows } from '@/stores/windows'
import WindowFrame from './WindowFrame.vue'

// A fake app with one focusable widget, standing in for the terminal.
const fakeComponent = defineComponent({ render: () => h('textarea', { class: 'widget' }) })
const fakeApp: AppManifest = {
  id: 'fake',
  name: 'Fake',
  comment: '',
  icon: 'utilities-terminal',
  category: 'Accessories',
  defaultSize: { w: 400, h: 300 },
  component: async () => fakeComponent,
}

vi.mock('@/apps/registry', () => ({
  getApp: (id: string) => (id === 'fake' ? fakeApp : undefined),
  appComponent: (id: string) => (id === 'fake' ? fakeComponent : undefined),
  appForMime: () => undefined,
}))

let host: HTMLElement
let unmount: () => void

beforeEach(() => {
  const pinia = createPinia()
  setActivePinia(pinia)
  host = document.createElement('div')
  document.body.append(host)
  const wm = useWindows()
  const id = wm.open('fake')!
  const app = createApp({ render: () => h(WindowFrame, { win: wm.get(id)! }) }).use(pinia)
  app.mount(host)
  unmount = () => app.unmount()
})

afterEach(() => {
  unmount()
  host.remove()
})

function press(el: Element): MouseEvent {
  const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0 })
  el.dispatchEvent(ev)
  return ev
}

describe('frame focus', () => {
  it('returns keyboard focus to the widget when the title bar is pressed', async () => {
    await nextTick()
    const widget = host.querySelector<HTMLTextAreaElement>('.widget')!
    widget.focus()
    widget.blur()
    expect(document.activeElement).toBe(document.body)

    const ev = press(host.querySelector('.titlebar .title')!)
    // The browser must not move focus to the body afterwards...
    expect(ev.defaultPrevented).toBe(true)
    // ...and the widget that had focus gets it back.
    expect(document.activeElement).toBe(widget)
  })

  it('does the same for title bar buttons and resize edges', async () => {
    await nextTick()
    const widget = host.querySelector<HTMLTextAreaElement>('.widget')!
    for (const sel of ['.wm-button', '.handle.se']) {
      widget.focus()
      widget.blur()
      expect(press(host.querySelector(sel)!).defaultPrevented).toBe(true)
      expect(document.activeElement).toBe(widget)
    }
  })

  it('leaves presses inside the app alone', async () => {
    await nextTick()
    const widget = host.querySelector<HTMLTextAreaElement>('.widget')!
    expect(press(widget).defaultPrevented).toBe(false)
  })
})
