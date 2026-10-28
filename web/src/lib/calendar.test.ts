import { describe, expect, it } from 'vitest'
import { firstDayOfWeek, monthGrid, sameDay, weekdayLabels } from './calendar'

const ymd = (d: Date) => `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`

describe('monthGrid', () => {
  it('fills six weeks starting on the first day of the week', () => {
    // October 2026 starts on a Thursday.
    const monday = monthGrid(2026, 9, 1)
    expect(monday).toHaveLength(6)
    expect(monday.every((w) => w.length === 7)).toBe(true)
    expect(ymd(monday[0]![0]!)).toBe('2026-9-28')
    expect(ymd(monday[0]![3]!)).toBe('2026-10-1')
    expect(ymd(monday[5]![6]!)).toBe('2026-11-8')

    const sunday = monthGrid(2026, 9, 0)
    expect(ymd(sunday[0]![0]!)).toBe('2026-9-27')
    expect(sunday[0]!.map((d) => d.getDay())).toEqual([0, 1, 2, 3, 4, 5, 6])
  })

  it('starts on the 1st when the month begins the week', () => {
    // June 2026 starts on a Monday.
    expect(ymd(monthGrid(2026, 5, 1)[0]![0]!)).toBe('2026-6-1')
  })

  it('counts every day once across a daylight saving change', () => {
    // March 2026: clocks change in Europe and America; days stay whole days.
    const days = monthGrid(2026, 2, 1).flat()
    for (let i = 1; i < days.length; i++) {
      const next = new Date(days[i - 1]!)
      next.setDate(next.getDate() + 1)
      expect(ymd(days[i]!)).toBe(ymd(next))
    }
  })
})

describe('firstDayOfWeek', () => {
  it('follows the locale', () => {
    expect(firstDayOfWeek('en-US')).toBe(0)
    expect(firstDayOfWeek('en-GB')).toBe(1)
    expect(firstDayOfWeek('de-DE')).toBe(1)
    expect(firstDayOfWeek('ar-EG')).toBe(6)
  })

  it('uses CLDR regions where the browser has no week info (Firefox)', () => {
    const proto = Intl.Locale.prototype as unknown as Record<string, unknown>
    const saved = ['getWeekInfo', 'weekInfo'].map((k) => [k, Object.getOwnPropertyDescriptor(proto, k)] as const)
    for (const [k] of saved) Object.defineProperty(proto, k, { value: undefined, configurable: true })
    try {
      expect(firstDayOfWeek('en-US')).toBe(0)
      expect(firstDayOfWeek('pt-BR')).toBe(0)
      expect(firstDayOfWeek('ar-EG')).toBe(6)
      expect(firstDayOfWeek('fr')).toBe(1)
      expect(firstDayOfWeek('en-GB')).toBe(1)
    } finally {
      for (const [k, d] of saved) {
        if (d) Object.defineProperty(proto, k, d)
        else delete proto[k]
      }
    }
  })

  it('falls back to Monday for tags it cannot read', () => {
    expect(firstDayOfWeek('not a locale!')).toBe(1)
  })
})

describe('weekdayLabels', () => {
  it('lists the days in grid order', () => {
    expect(weekdayLabels('en-US', 1)).toEqual(['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'])
    expect(weekdayLabels('en-US', 0)[0]).toBe('Sun')
  })
})

describe('sameDay', () => {
  it('ignores the time of day', () => {
    expect(sameDay(new Date(2026, 9, 27, 0, 1), new Date(2026, 9, 27, 23, 59))).toBe(true)
    expect(sameDay(new Date(2026, 9, 27), new Date(2026, 10, 27))).toBe(false)
  })
})
