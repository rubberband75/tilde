import { describe, expect, it } from 'vitest'
import { matchShortcut } from './shortcuts'

function key(code: string, mods: { ctrl?: boolean; alt?: boolean; shift?: boolean; meta?: boolean } = {}) {
  return new KeyboardEvent('keydown', {
    code,
    ctrlKey: !!mods.ctrl,
    altKey: !!mods.alt,
    shiftKey: !!mods.shift,
    metaKey: !!mods.meta,
  })
}

const pc = { superKeys: true }
const mac = { superKeys: false }

describe('matchShortcut', () => {
  it.each([
    ['Tab', { alt: true }, 'switch'],
    ['Tab', { alt: true, shift: true }, 'switch-back'],
    ['Backquote', { alt: true }, 'switch'],
    ['Backquote', { alt: true, shift: true }, 'switch-back'],
    ['F1', { alt: true }, 'menu'],
    ['ArrowLeft', { meta: true }, 'tile-left'],
    ['ArrowLeft', { alt: true, shift: true }, 'tile-left'],
    ['ArrowUp', { alt: true, shift: true }, 'tile-up'],
    ['KeyD', { meta: true }, 'show-desktop'],
    ['KeyD', { alt: true, shift: true }, 'show-desktop'],
    ['F4', { alt: true }, 'close'],
    ['F4', { alt: true, shift: true }, 'close'],
    ['Space', { alt: true }, 'window-menu'],
    ['Space', { alt: true, shift: true }, 'window-menu'],
    ['F7', { alt: true }, 'move'],
    ['F8', { alt: true }, 'resize'],
    ['F10', { alt: true }, 'maximize'],
    ['KeyT', { ctrl: true, alt: true }, 'terminal'],
    ['KeyL', { ctrl: true, alt: true }, 'lock'],
  ] as const)('%s with %j is %s', (code, mods, want) => {
    expect(matchShortcut(key(code, mods), pc)).toBe(want)
  })

  it('leaves other keys to the apps', () => {
    for (const [code, mods] of [
      ['KeyD', {}],
      ['ArrowLeft', {}],
      ['ArrowLeft', { alt: true }],
      ['ArrowLeft', { shift: true }],
      ['Tab', {}],
      ['Tab', { ctrl: true }],
      ['KeyL', { ctrl: true }],
      ['KeyD', { meta: true, shift: true }],
    ] as const) {
      expect(matchShortcut(key(code, mods), pc)).toBeNull()
    }
  })

  it('leaves Command to the apps on a Mac', () => {
    expect(matchShortcut(key('ArrowLeft', { meta: true }), mac)).toBeNull()
    expect(matchShortcut(key('KeyD', { meta: true }), mac)).toBeNull()
    expect(matchShortcut(key('ArrowLeft', { alt: true, shift: true }), mac)).toBe('tile-left')
  })

  it('does not take AltGr, which Windows reports as Ctrl+Alt', () => {
    const altGr = key('KeyL', { ctrl: true, alt: true })
    Object.defineProperty(altGr, 'getModifierState', { value: (k: string) => k === 'AltGraph' })
    expect(matchShortcut(altGr, pc)).toBeNull()
  })

  it('does not take keys while an input method composes', () => {
    const e = new KeyboardEvent('keydown', { code: 'Tab', altKey: true, isComposing: true })
    expect(matchShortcut(e, pc)).toBeNull()
  })
})
