// Short names for browsers from their User-Agent header, for the list of
// the user's signed-in browsers (D47). Only for showing: any page can send
// any User-Agent.

const browsers: [RegExp, string][] = [
  [/Edg(e|A|iOS)?\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/Vivaldi\//, 'Vivaldi'],
  [/Firefox\/|FxiOS\//, 'Firefox'],
  [/Chrome\/|CriOS\//, 'Chrome'],
  [/Safari\//, 'Safari'],
]

const systems: [RegExp, string][] = [
  [/Android/, 'Android'],
  [/iPhone|iPad|iPod/, 'iOS'],
  [/CrOS/, 'ChromeOS'],
  [/Windows/, 'Windows'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/Linux|X11/, 'Linux'],
]

/** "Firefox on Linux"; "Unknown browser" when nothing matches. */
export function describeAgent(ua: string): string {
  const browser = browsers.find(([re]) => re.test(ua))?.[1]
  const system = systems.find(([re]) => re.test(ua))?.[1]
  if (browser && system) return `${browser} on ${system}`
  return browser ?? (system ? `A browser on ${system}` : 'Unknown browser')
}

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago". */
export function ago(iso: string, now: number = Date.now()): string {
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000)
  if (s < 60) return 'just now'
  const unit = (n: number, what: string) => `${n} ${what}${n === 1 ? '' : 's'} ago`
  if (s < 3600) return unit(Math.floor(s / 60), 'minute')
  if (s < 86400) return unit(Math.floor(s / 3600), 'hour')
  return unit(Math.floor(s / 86400), 'day')
}
