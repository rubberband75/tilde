import { createPinia, setActivePinia } from 'pinia'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppManifest } from '@/apps/types'
import { useWindows } from '@/stores/windows'
import { boxTransform, GEOMETRY_MS, MINIMIZE_MS, sameBox } from './animate'
import WindowFrame from './WindowFrame.vue'

describe('boxTransform', () => {
  it('draws the laid-out box in another box, from the top left', () => {
    const at = { left: 100, top: 50, width: 400, height: 300 }
    expect(boxTransform(at, at)).toBe('translate(0px, 0px) scale(1, 1)')
    expect(boxTransform({ left: 0, top: 0, width: 800, height: 150 }, at)).toBe(
      'translate(-100px, -50px) scale(2, 0.5)',
    )
  })

  it('treats boxes a pixel apart as the same', () => {
    expect(sameBox({ left: 0, top: 0, width: 10, height: 10 }, { left: 0.4, top: 0, width: 10.5, height: 10 })).toBe(
      true,
    )
    expect(sameBox({ left: 0, top: 0, width: 10, height: 10 }, { left: 0, top: 0, width: 12, height: 10 })).toBe(false)
  })
})

const fakeApp: AppManifest = {
  id: 'fake',
  name: 'Fake',
  comment: '',
  icon: 'utilities-terminal',
  category: 'Accessories',
  defaultSize: { w: 400, h: 300 },
  component: async () => defineComponent({ render: () => h('div') }),
}
vi.mock('@/apps/registry', () => ({
  getApp: (id: string) => (id === 'fake' ? fakeApp : undefined),
  appComponent: () => undefined,
  appForMime: () => undefined,
}))

interface Played {
  frames: Keyframe[]
  duration: number
  finish: () => void
}

describe('frame animations', () => {
  let host: HTMLElement
  let unmount: () => void
  let played: Played[]
  let id: string

  beforeEach(() => {
    const pinia = createPinia()
    setActivePinia(pinia)
    played = []
    // jsdom does no layout: a frame's box is its inline geometry, and the
    // window-list button sits at the bottom left.
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.dataset.window) return new DOMRect(10, 770, 150, 30)
      const s = this.style
      if (s.display === 'none' || this.classList.contains('minimized')) return new DOMRect(0, 0, 0, 0)
      return new DOMRect(parseFloat(s.left), parseFloat(s.top), parseFloat(s.width), parseFloat(s.height))
    })
    HTMLElement.prototype.animate = function (
      frames: Keyframe[] | PropertyIndexedKeyframes | null,
      opts?: number | KeyframeAnimationOptions,
    ) {
      const a = { cancel: vi.fn(), onfinish: null as ((e: AnimationPlaybackEvent) => void) | null }
      played.push({
        frames: frames as Keyframe[],
        duration: (opts as KeyframeAnimationOptions).duration as number,
        finish: () => a.onfinish?.({} as AnimationPlaybackEvent),
      })
      return a as unknown as Animation
    }
    vi.stubGlobal('matchMedia', () => ({ matches: false }))

    const wm = useWindows()
    wm.setArea(1200, 760)
    id = wm.open('fake')!
    host = document.createElement('div')
    const button = document.createElement('button')
    button.dataset.window = id
    host.append(button)
    document.body.append(host)
    const mountAt = document.createElement('div')
    host.append(mountAt)
    const app = createApp({ render: () => h(WindowFrame, { win: wm.get(id)! }) }).use(pinia)
    app.mount(mountAt)
    unmount = () => app.unmount()
  })

  afterEach(() => {
    unmount()
    host.remove()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    delete (HTMLElement.prototype as Partial<HTMLElement>).animate
  })

  const frame = () => host.querySelector<HTMLElement>('.window')!

  it('plays maximizing and restoring from where the window was', async () => {
    const wm = useWindows()
    const before = { ...wm.get(id)!.rect }
    wm.toggleMaximize(id)
    await nextTick()
    await nextTick()
    expect(played).toHaveLength(1)
    expect(played[0]!.duration).toBe(GEOMETRY_MS)
    expect(played[0]!.frames[0]!.transform).toBe(
      boxTransform(
        { left: before.x, top: before.y, width: before.w, height: before.h },
        { left: 0, top: 0, width: 1200, height: 760 },
      ),
    )
    expect(played[0]!.frames[1]!.transform).toBe('none')

    wm.toggleMaximize(id)
    await nextTick()
    await nextTick()
    expect(played).toHaveLength(2)
  })

  it('shrinks a minimized window into its button, and only then hides it', async () => {
    const wm = useWindows()
    wm.minimize(id)
    await nextTick()
    await nextTick()
    expect(played).toHaveLength(1)
    expect(played[0]!.duration).toBe(MINIMIZE_MS)
    expect(played[0]!.frames[1]).toMatchObject({ opacity: 0 })
    expect(played[0]!.frames[1]!.transform).toContain('translate(')
    expect(frame().classList.contains('minimized')).toBe(false)
    expect(frame().classList.contains('leaving')).toBe(true)

    played[0]!.finish()
    await nextTick()
    expect(frame().classList.contains('minimized')).toBe(true)

    // And grows back out of it.
    wm.focus(id)
    await nextTick()
    await nextTick()
    expect(played).toHaveLength(2)
    expect(played[1]!.frames[0]).toMatchObject({ opacity: 0 })
    expect(played[1]!.frames[1]).toMatchObject({ transform: 'none', opacity: 1 })
  })

  it('leaves drags and reduced motion alone', async () => {
    const wm = useWindows()
    wm.interacting = true
    wm.toggleMaximize(id)
    await nextTick()
    await nextTick()
    wm.interacting = false
    vi.stubGlobal('matchMedia', () => ({ matches: true }))
    wm.toggleMaximize(id)
    wm.minimize(id)
    await nextTick()
    await nextTick()
    expect(played).toHaveLength(0)
    expect(frame().classList.contains('minimized')).toBe(true)
  })
})
