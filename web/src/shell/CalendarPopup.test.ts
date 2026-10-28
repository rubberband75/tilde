import { createApp, h, nextTick, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CalendarPopup from './CalendarPopup.vue'

let host: HTMLElement
let unmount: () => void
const now = ref(new Date(2026, 9, 27, 14, 30))

beforeEach(() => {
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB')
  now.value = new Date(2026, 9, 27, 14, 30)
  host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ render: () => h(CalendarPopup, { now: now.value }) })
  app.mount(host)
  unmount = () => app.unmount()
})

afterEach(() => {
  unmount()
  host.remove()
  vi.restoreAllMocks()
})

const text = (sel: string) => host.querySelector(sel)!.textContent!.trim()
const button = (label: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!

describe('calendar popup', () => {
  it("shows today's date and month, weeks starting on the locale's first day", () => {
    expect(text('.weekday')).toBe('Tuesday')
    expect(text('.full-date')).toBe('27 October 2026')
    expect(text('.month-label')).toBe('October 2026')
    expect(text('th')).toBe('Mon')
    const today = host.querySelector('td[aria-current="date"]')!
    expect(today.textContent!.trim()).toBe('27')
    expect(host.querySelectorAll('td.other')).toHaveLength(42 - 31)
  })

  it('pages through months, and comes back to this one', async () => {
    button('Next month').click()
    button('Next month').click()
    button('Next month').click()
    await nextTick()
    expect(text('.month-label')).toBe('January 2027')
    expect(host.querySelector('td[aria-current]')).toBeNull()
    host.querySelector('.month')!.dispatchEvent(new WheelEvent('wheel', { deltaY: -100, cancelable: true }))
    await nextTick()
    expect(text('.month-label')).toBe('December 2026')
    host.querySelector<HTMLButtonElement>('.month-label')!.click()
    await nextTick()
    expect(text('.month-label')).toBe('October 2026')
  })

  it('moves on to the next month at midnight when it shows this one', async () => {
    now.value = new Date(2026, 10, 1, 0, 0)
    await nextTick()
    expect(text('.month-label')).toBe('November 2026')
    expect(host.querySelector('td[aria-current="date"]')!.textContent!.trim()).toBe('1')
  })
})
