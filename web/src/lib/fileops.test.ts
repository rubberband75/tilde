import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { FileClipboard } from '@/apps/types'
import { deletePermanently, moveToTrash, paste, type FileOpsHost } from './fileops'

const start = vi.fn()
const trash = vi.fn()

vi.mock('@/api/jobs', () => ({
  jobsApi: {
    start: (...args: unknown[]) => {
      start(...args)
      return Promise.resolve({ id: 'j1', sources: [], started: '' })
    },
  },
}))
vi.mock('@/api/fs', async (orig) => ({
  ...(await orig<typeof import('@/api/fs')>()),
  fsApi: { trash: (paths: string[]) => trash(paths) },
}))

function makeHost(answer: boolean) {
  const clip: FileClipboard & { op: FileClipboard['op']; paths: string[] } = {
    op: null,
    paths: [],
    set(op, paths) {
      this.op = op
      this.paths = paths
    },
    clear() {
      this.op = null
      this.paths = []
    },
  }
  const host: FileOpsHost = { confirm: vi.fn(async () => answer), notify: vi.fn(), clipboard: clip }
  return host
}

beforeEach(() => {
  setActivePinia(createPinia())
  start.mockClear()
  trash.mockReset()
})

describe('paste', () => {
  it('copies, and moves what was cut, then forgets the cut', async () => {
    const host = makeHost(true)
    host.clipboard.set('copy', ['/a/x'])
    await paste(host, '/b')
    expect(start).toHaveBeenLastCalledWith('copy', ['/a/x'], '/b')
    expect(host.clipboard.op).toBe('copy')

    host.clipboard.set('cut', ['/a/y'])
    await paste(host, '/b')
    expect(start).toHaveBeenLastCalledWith('move', ['/a/y'], '/b')
    expect(host.clipboard.op).toBeNull()
  })

  it('does nothing with an empty clipboard', async () => {
    await paste(makeHost(true), '/b')
    expect(start).not.toHaveBeenCalled()
  })
})

describe('deletePermanently', () => {
  it('only deletes after confirmation', async () => {
    await deletePermanently(makeHost(false), ['/a/x'])
    expect(start).not.toHaveBeenCalled()
    const host = makeHost(true)
    await deletePermanently(host, ['/a/x'])
    expect(host.confirm).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Permanently delete “x”?', danger: true }),
    )
    expect(start).toHaveBeenCalledWith('delete', ['/a/x'], undefined)
  })
})

describe('moveToTrash', () => {
  it('offers to delete items that cannot be trashed', async () => {
    trash.mockResolvedValue([
      { path: '/home/me/a', name: 'a' },
      { path: '/media/usb/b', error: 'other drive', code: 'other_drive' },
    ])
    const host = makeHost(true)
    await moveToTrash(host, ['/home/me/a', '/media/usb/b'], '/home/me/.local/share/Trash/files')
    expect(host.confirm).toHaveBeenCalledOnce()
    expect(start).toHaveBeenCalledWith('delete', ['/media/usb/b'], undefined)
    expect(host.notify).not.toHaveBeenCalled()
  })

  it('reports other failures without asking', async () => {
    trash.mockResolvedValue([{ path: '/etc/x', error: 'permission denied', code: 'permission_denied' }])
    const host = makeHost(true)
    await moveToTrash(host, ['/etc/x'], '/t')
    expect(host.confirm).not.toHaveBeenCalled()
    expect(host.notify).toHaveBeenCalledWith(expect.stringContaining('permission denied'), 'error')
  })
})
