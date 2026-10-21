import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { fsApi } from '@/api/fs'
import type { Background } from '@/api/types'

/**
 * CSS for a background, as Cinnamon draws it: the picture (if any) over a
 * solid color or a two-color gradient. Nothing for the "default" source, so
 * the theme's background shows.
 */
export function backgroundStyle(bg: Background | null, url: (path: string) => string): Record<string, string> {
  if (!bg || bg.source === 'default') return {}
  const layers: string[] = []
  const sizes: string[] = []
  const positions: string[] = []
  const repeats: string[] = []
  if (bg.picture && bg.options !== 'none') {
    layers.push(`url(${JSON.stringify(url(bg.picture))})`)
    const [size, position, repeat] = {
      zoom: ['cover', 'center', 'no-repeat'],
      spanned: ['cover', 'center', 'no-repeat'],
      scaled: ['contain', 'center', 'no-repeat'],
      centered: ['auto', 'center', 'no-repeat'],
      stretched: ['100% 100%', 'center', 'no-repeat'],
      wallpaper: ['auto', 'top left', 'repeat'],
    }[bg.options]
    sizes.push(size)
    positions.push(position)
    repeats.push(repeat)
  }
  if (bg.shading !== 'solid') {
    const dir = bg.shading === 'vertical' ? 'to bottom' : 'to right'
    layers.push(`linear-gradient(${dir}, ${bg.primary}, ${bg.secondary})`)
    sizes.push('100% 100%')
    positions.push('center')
    repeats.push('no-repeat')
  }
  return {
    backgroundColor: bg.primary,
    backgroundImage: layers.join(', ') || 'none',
    backgroundSize: sizes.join(', ') || 'auto',
    backgroundPosition: positions.join(', ') || 'center',
    backgroundRepeat: repeats.join(', ') || 'no-repeat',
  }
}

/** The desktop background: Cinnamon's, or one picked in Tilde (D37). */
export const useBackground = defineStore('background', () => {
  const current = ref<Background | null>(null)
  const style = computed(() => backgroundStyle(current.value, (p) => fsApi.contentUrl(p)))

  async function load() {
    try {
      current.value = await fsApi.background()
    } catch {
      // The theme's background stays.
    }
  }

  /** Changes Tilde's background, starting from what is shown now. */
  async function set(change: Partial<Omit<Background, 'source'>>) {
    const base = current.value ?? {
      picture: '',
      options: 'zoom',
      primary: '#000000',
      secondary: '#000000',
      shading: 'solid',
    }
    const next = { ...base, ...change }
    current.value = { ...next, source: 'tilde' }
    current.value = await fsApi.setBackground({
      picture: next.picture,
      options: next.options,
      primary: next.primary,
      secondary: next.secondary,
      shading: next.shading,
    })
  }

  async function reset() {
    current.value = await fsApi.resetBackground()
  }

  function clear() {
    current.value = null
  }

  return { current, style, load, set, reset, clear }
})
