// Month grids for the panel's calendar (D54), in the browser's locale and
// time zone, as the clock shows the time. Dates are local Date objects,
// built from year, month and day so a daylight saving change never shifts
// a day.

/**
 * Regions whose week starts on Sunday or Saturday (CLDR), for browsers
 * without Intl.Locale's week info (Firefox). Everywhere else it is Monday.
 */
const sundayRegions = new Set(
  'AG AS BD BR BS BT BW BZ CA CN CO DM DO ET GT GU HK HN ID IL IN JM JP KE KH KR LA MH MM MO MT MX MZ NI NP PA PE PH PK PR PT PY SA SG SV TH TT TW UM US VE VI WS YE ZA ZW'.split(
    ' ',
  ),
)
const saturdayRegions = new Set('AE AF BH DJ DZ EG IQ IR JO KW LY OM QA SD SY'.split(' '))

interface WeekInfo {
  /** 1 is Monday, 7 is Sunday. */
  firstDay: number
}

/** The first day of the week for a locale, as Date.getDay() counts: 0 is Sunday. */
export function firstDayOfWeek(locale: string): number {
  let loc: Intl.Locale
  try {
    loc = new Intl.Locale(locale)
  } catch {
    return 1
  }
  const withInfo = loc as Intl.Locale & { getWeekInfo?: () => WeekInfo; weekInfo?: WeekInfo }
  const info = withInfo.getWeekInfo?.() ?? withInfo.weekInfo
  if (info) return info.firstDay % 7
  const region = loc.maximize().region ?? ''
  if (sundayRegions.has(region)) return 0
  if (saturdayRegions.has(region)) return 6
  return 1
}

/**
 * The six weeks shown for a month (0-based, as Date counts), starting on
 * firstDay: the days before and after the month fill the first and last
 * rows, so every month takes the same space.
 */
export function monthGrid(year: number, month: number, firstDay: number): Date[][] {
  const lead = (new Date(year, month, 1).getDay() - firstDay + 7) % 7
  const weeks: Date[][] = []
  for (let w = 0; w < 6; w++) {
    const week: Date[] = []
    for (let d = 0; d < 7; d++) week.push(new Date(year, month, 1 - lead + w * 7 + d))
    weeks.push(week)
  }
  return weeks
}

/** Short weekday names in grid order. */
export function weekdayLabels(locale: string, firstDay: number): string[] {
  const fmt = new Intl.DateTimeFormat(locale, { weekday: 'short' })
  // 2024-01-07 was a Sunday.
  return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(2024, 0, 7 + ((firstDay + i) % 7))))
}

export function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
}
