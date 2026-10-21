import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { setWatchSender, useWatchedDirs, watchDir, watchedDirs } from './fswatch'

const flush = () => new Promise<void>((r) => queueMicrotask(r))

afterEach(() => setWatchSender(undefined))

describe('fswatch', () => {
  it('counts folders and sends one batched set per change', async () => {
    const send = vi.fn()
    setWatchSender(send)
    await flush()
    expect(send).toHaveBeenLastCalledWith([])

    const a1 = watchDir('/a')
    const a2 = watchDir('/a')
    const b = watchDir('/b')
    await flush()
    expect(send).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenLastCalledWith(['/a', '/b'])

    a1()
    a1()
    await flush()
    expect(send).toHaveBeenCalledTimes(2)
    expect(watchedDirs()).toEqual(['/a', '/b'])

    a2()
    b()
    await flush()
    expect(send).toHaveBeenLastCalledWith([])
  })

  it('follows a component from folder to folder and releases on unmount', async () => {
    const send = vi.fn()
    setWatchSender(send)
    const dir = ref('/home/me')
    const Comp = defineComponent({
      setup() {
        useWatchedDirs(() => [dir.value, '/trash', ''])
        return () => h('div')
      },
    })
    const app = createApp(Comp)
    app.mount(document.createElement('div'))
    await flush()
    expect(send).toHaveBeenLastCalledWith(['/home/me', '/trash'])

    dir.value = '/tmp'
    await nextTick()
    await flush()
    expect(send).toHaveBeenLastCalledWith(['/trash', '/tmp'])

    app.unmount()
    await flush()
    expect(send).toHaveBeenLastCalledWith([])
  })
})
