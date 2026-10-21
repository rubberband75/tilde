import { describe, expect, it } from 'vitest'
import type { Background } from '@/api/types'
import { backgroundStyle } from './background'

const bg = (over: Partial<Background>): Background => ({
  source: 'cinnamon',
  picture: '/usr/share/backgrounds/a b.jpg',
  options: 'zoom',
  primary: '#112233',
  secondary: '#445566',
  shading: 'solid',
  ...over,
})
const url = (p: string) => `/content?path=${encodeURIComponent(p)}`

describe('backgroundStyle', () => {
  it('leaves the theme background alone without a setting', () => {
    expect(backgroundStyle(null, url)).toEqual({})
    expect(backgroundStyle(bg({ source: 'default' }), url)).toEqual({})
  })

  it('zooms a picture over the color', () => {
    expect(backgroundStyle(bg({}), url)).toEqual({
      backgroundColor: '#112233',
      backgroundImage: 'url("/content?path=%2Fusr%2Fshare%2Fbackgrounds%2Fa%20b.jpg")',
      backgroundSize: 'cover',
      backgroundPosition: 'center',
      backgroundRepeat: 'no-repeat',
    })
  })

  it('tiles, and layers a picture over a gradient', () => {
    const s = backgroundStyle(bg({ options: 'wallpaper', shading: 'vertical' }), url)
    expect(s.backgroundImage).toMatch(/^url\(.+\), linear-gradient\(to bottom, #112233, #445566\)$/)
    expect(s.backgroundRepeat).toBe('repeat, no-repeat')
    expect(s.backgroundSize).toBe('auto, 100% 100%')
  })

  it('shows only colors without a picture', () => {
    expect(backgroundStyle(bg({ options: 'none', shading: 'horizontal' }), url).backgroundImage).toBe(
      'linear-gradient(to right, #112233, #445566)',
    )
    expect(backgroundStyle(bg({ picture: '' }), url).backgroundImage).toBe('none')
  })
})
