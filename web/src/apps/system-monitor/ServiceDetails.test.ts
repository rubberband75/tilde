import { createApp, h, nextTick, reactive } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { JournalEntry, Service } from '@/api/types'
import type { AppContext } from '@/apps/types'
import ServiceDetails from './ServiceDetails.vue'

const journal = vi.hoisted(() => vi.fn())
vi.mock('@/api/services', () => ({ servicesApi: { journal, setBoot: vi.fn() } }))

const ROW = 20
const lines = (from: number, to: number): JournalEntry[] =>
  Array.from({ length: to - from + 1 }, (_, i) => ({
    time: (from + i) * 1000,
    priority: 6,
    message: `line ${from + i}`,
  }))

// jsdom does no layout: each line is ROW tall, the log shows 5 of them.
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('entry')
      ? [...this.parentElement!.querySelectorAll('.entry')].indexOf(this) * ROW
      : 0
  })
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('entry') ? ROW : 0
  })
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.querySelectorAll('.entry').length * ROW
  })
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(5 * ROW)
})

let host: HTMLElement
let unmount: () => void
const props = reactive<{ service: Service }>({
  service: { name: 'cups.service', active: 'active', sub: 'running', boot: 'enabled', load: 'loaded' },
})

function mount() {
  host = document.createElement('div')
  document.body.append(host)
  const ctx = { notify: vi.fn() } as unknown as AppContext
  const app = createApp({ render: () => h(ServiceDetails, { ctx, scope: 'system', service: props.service }) })
  app.mount(host)
  unmount = () => app.unmount()
}

afterEach(() => {
  unmount()
  host.remove()
  vi.restoreAllMocks()
  journal.mockReset()
  props.service = { name: 'cups.service', active: 'active', sub: 'running', boot: 'enabled', load: 'loaded' }
})

const flush = async () => {
  for (let i = 0; i < 4; i++) await nextTick()
}
const log = () => host.querySelector<HTMLElement>('[role="log"]')!

describe('service details journal', () => {
  it('reloads only for a change of state, not each refresh of the list', async () => {
    journal.mockResolvedValue({ entries: lines(1, 20), limited: false })
    mount()
    await flush()
    expect(journal).toHaveBeenCalledTimes(1)
    expect(log().scrollTop).toBe(20 * ROW) // opens at the end
    // The list refreshed: a new object, the same service in the same state.
    props.service = { ...props.service }
    await flush()
    expect(journal).toHaveBeenCalledTimes(1)
  })

  it('keeps the line being read while scrolled up, and follows when at the end', async () => {
    journal.mockResolvedValueOnce({ entries: lines(1, 20), limited: false })
    mount()
    await flush()
    // Scrolled up to read line 6 at the top.
    log().scrollTop = 5 * ROW
    // The service restarted: three new lines, the three oldest dropped.
    journal.mockResolvedValueOnce({ entries: lines(4, 23), limited: false })
    props.service = { ...props.service, active: 'activating', sub: 'start' }
    await flush()
    expect(journal).toHaveBeenCalledTimes(2)
    // line 6 is now the third line: still at the top of the view.
    expect(log().scrollTop).toBe(2 * ROW)

    // At the end: new lines come into view.
    log().scrollTop = log().scrollHeight - log().clientHeight
    journal.mockResolvedValueOnce({ entries: lines(4, 30), limited: false })
    props.service = { ...props.service, active: 'active', sub: 'running' }
    await flush()
    expect(log().scrollTop).toBe(27 * ROW)
  })
})
