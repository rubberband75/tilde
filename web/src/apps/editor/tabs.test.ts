import { describe, expect, it } from 'vitest'
import { afterClose, moveTab, newTab, openPath, tabKey, tabsFromArgs, type TabSpec, type Tabs } from './tabs'

const tab = (id: string, path?: string): TabSpec => ({ id, path, draft: `d-${id}` })
const ids = (s: Tabs) => s.tabs.map((t) => t.id)

describe('tabsFromArgs', () => {
  it('reads a window with tabs', () => {
    const s = tabsFromArgs({ tabs: [tab('a', '/x.txt'), { id: 'b', draft: 'db', wrap: true }, 'junk'], active: 1 })
    expect(s.tabs.map((t) => [t.id, t.path, t.draft, t.wrap])).toEqual([
      ['a', '/x.txt', 'd-a', undefined],
      ['b', undefined, 'db', true],
    ])
    expect(s.active).toBe(1)
  })

  it('turns a window from before tabs into one tab', () => {
    const s = tabsFromArgs({ path: '/notes.txt', draft: 'abc', wrap: true })
    expect(s.tabs).toHaveLength(1)
    expect(s.tabs[0]).toMatchObject({ path: '/notes.txt', draft: 'abc', wrap: true })
    expect(tabsFromArgs({ draft: 'only' }).tabs[0]).toMatchObject({ draft: 'only' })
  })

  it('always has a tab, and an active one in range', () => {
    expect(tabsFromArgs({}).tabs).toHaveLength(1)
    expect(tabsFromArgs({ tabs: [], active: 3 }).active).toBe(0)
    expect(tabsFromArgs({ tabs: [tab('a'), tab('b')], active: 9 }).active).toBe(1)
  })
})

describe('opening and closing tabs', () => {
  const state: Tabs = { tabs: [tab('a', '/a.txt'), tab('b')], active: 1 }

  it('switches to a file already open', () => {
    expect(openPath(state, '/a.txt', () => true)).toEqual({ tabs: state.tabs, active: 0 })
  })

  it('takes the place of an untouched empty tab, else opens after the active one', () => {
    const replaced = openPath(state, '/c.txt', (t) => t.id === 'b')
    expect(replaced.tabs.map((t) => t.path)).toEqual(['/a.txt', '/c.txt'])
    expect(replaced.active).toBe(1)
    const added = openPath({ ...state, active: 0 }, '/c.txt', () => false)
    expect(added.tabs.map((t) => t.path)).toEqual(['/a.txt', '/c.txt', undefined])
    expect(added.active).toBe(1)
  })

  it('goes to the next tab after closing the active one, or the one before', () => {
    const three: Tabs = { tabs: [tab('a'), tab('b'), tab('c')], active: 1 }
    expect(afterClose(three, 'b')).toEqual({ tabs: [tab('a'), tab('c')], active: 1 })
    expect(afterClose({ ...three, active: 2 }, 'c').active).toBe(1)
    expect(afterClose({ ...three, active: 2 }, 'a')).toEqual({ tabs: [tab('b'), tab('c')], active: 1 })
  })

  it('reorders, keeping the active tab active', () => {
    const three: Tabs = { tabs: [tab('a'), tab('b'), tab('c')], active: 0 }
    const moved = moveTab(three, 0, 3)
    expect(ids(moved)).toEqual(['b', 'c', 'a'])
    expect(moved.active).toBe(2)
    expect(ids(moveTab(three, 2, 0))).toEqual(['c', 'a', 'b'])
  })

  it('names drafts as the document does', () => {
    expect(tabKey({ path: '/a.txt', draft: 'x' })).toBe('file:/a.txt')
    expect(tabKey({ draft: 'x' })).toBe('new:x')
    expect(newTab().id).not.toBe(newTab().id)
  })
})
