import { beforeEach, describe, expect, it } from 'vitest'
import { applyLook, rememberedLook, resolveLook, themeAttributes } from './theme'

beforeEach(() => localStorage.clear())

describe('the look', () => {
  it('follows the desktop where the user leaves it to the desktop', () => {
    const desktop = { source: 'cinnamon' as const, mode: 'mixed' as const, accent: 'aqua' as const }
    expect(resolveLook({ mode: 'desktop', accent: 'desktop' }, desktop)).toEqual({ mode: 'mixed', accent: 'aqua' })
    expect(resolveLook({ mode: 'light', accent: 'desktop' }, desktop)).toEqual({ mode: 'light', accent: 'aqua' })
    expect(resolveLook({ mode: 'desktop', accent: 'red' }, null)).toEqual({ mode: 'dark', accent: 'red' })
  })

  it('makes mixed light windows with a dark panel', () => {
    expect(themeAttributes({ mode: 'mixed', accent: 'teal' })).toEqual({ shell: 'dark', apps: 'light', accent: 'teal' })
    expect(themeAttributes({ mode: 'light', accent: 'green' })).toMatchObject({ shell: 'light', apps: 'light' })
    expect(themeAttributes({ mode: 'dark', accent: 'green' })).toMatchObject({ shell: 'dark', apps: 'dark' })
  })

  it('remembers the last look for the login screen, and ignores junk', () => {
    expect(rememberedLook()).toEqual({ mode: 'dark', accent: 'green' })
    const el = document.createElement('html')
    applyLook({ mode: 'light', accent: 'pink' }, el)
    expect([el.dataset.shell, el.dataset.apps, el.dataset.accent]).toEqual(['light', 'light', 'pink'])
    expect(rememberedLook()).toEqual({ mode: 'light', accent: 'pink' })
    localStorage.setItem('tilde:look', '{"mode":"neon","accent":"pink"}')
    expect(rememberedLook()).toEqual({ mode: 'dark', accent: 'green' })
  })
})
