import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Notification, NotificationsState } from '@/api/types'
import { maxToasts, useNotifications } from './notifications'

const list = vi.fn<() => Promise<NotificationsState>>()
const action = vi.fn<(id: number, key: string) => Promise<void>>()
const close = vi.fn<(id: number) => Promise<void>>()
const clear = vi.fn<(ids?: number[]) => Promise<void>>()
vi.mock('@/api/notifications', () => ({
  notificationsApi: {
    list: () => list(),
    action: (id: number, key: string) => action(id, key),
    close: (id: number) => close(id),
    clear: (ids?: number[]) => clear(ids),
  },
}))

const note = (id: number, open = true): Notification => ({
  id,
  app: 'App',
  summary: `n${id}`,
  urgency: 1,
  time: id,
  open,
})

beforeEach(() => {
  setActivePinia(createPinia())
  for (const f of [list, action, close, clear]) f.mockReset()
  action.mockResolvedValue()
  close.mockResolvedValue()
  clear.mockResolvedValue()
})

describe('useNotifications', () => {
  it('shows the newest open ones as toasts, a few at a time', async () => {
    list.mockResolvedValue({ active: true, notifications: [note(5), note(4, false), note(3), note(2), note(1)] })
    const s = useNotifications()
    await s.refresh()
    expect(s.active).toBe(true)
    expect(s.toasts.map((n) => n.id)).toEqual([5, 3, 2].slice(0, maxToasts))
    expect(s.items).toHaveLength(5)
  })

  it('fetches once more for a burst of events, not once each', async () => {
    let answer!: (v: NotificationsState) => void
    list.mockImplementation(() => new Promise((r) => (answer = r)))
    const s = useNotifications()
    const first = s.refresh()
    void s.refresh()
    void s.refresh()
    answer({ active: true, notifications: [note(1)] })
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(2))
    answer({ active: true, notifications: [note(2), note(1)] })
    await first
    expect(list).toHaveBeenCalledTimes(2)
    expect(s.items.map((n) => n.id)).toEqual([2, 1])
  })

  it('hides a toast at once when clicked or dismissed, then follows the worker', async () => {
    list.mockResolvedValue({ active: true, notifications: [note(2), note(1)] })
    const s = useNotifications()
    await s.refresh()
    list.mockResolvedValue({ active: true, notifications: [note(2, false), note(1)] })
    const done = s.invoke(2, 'default')
    expect(s.toasts.map((n) => n.id)).toEqual([1])
    await done
    expect(action).toHaveBeenCalledWith(2, 'default')

    void s.dismiss(1)
    expect(s.toasts).toEqual([])
    expect(close).toHaveBeenCalledWith(1)
  })

  it('removes one notification, or all', async () => {
    list.mockResolvedValue({ active: true, notifications: [note(2), note(1)] })
    const s = useNotifications()
    await s.refresh()
    list.mockResolvedValue({ active: true, notifications: [note(1)] })
    const one = s.clear(2)
    expect(s.items.map((n) => n.id)).toEqual([1])
    await one
    expect(clear).toHaveBeenCalledWith([2])
    list.mockResolvedValue({ active: true, notifications: [] })
    await s.clear()
    expect(clear).toHaveBeenLastCalledWith(undefined)
    expect(s.items).toEqual([])
  })

  it('forgets everything when the events socket closes', async () => {
    list.mockResolvedValue({ active: true, notifications: [note(1)] })
    const s = useNotifications()
    await s.refresh()
    s.reset()
    expect(s.active).toBe(false)
    expect(s.items).toEqual([])
  })
})
