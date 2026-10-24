import { describe, expect, it } from 'vitest'
import { ago, describeAgent } from './agent'

describe('describeAgent', () => {
  it.each([
    ['Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0', 'Firefox on Linux'],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0',
      'Edge on Windows',
    ],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
      'Safari on macOS',
    ],
    [
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36',
      'Chrome on Android',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1',
      'Chrome on iOS',
    ],
    ['curl/8.5.0', 'Unknown browser'],
    ['', 'Unknown browser'],
  ])('%s', (ua, want) => expect(describeAgent(ua)).toBe(want))
})

describe('ago', () => {
  const now = Date.parse('2026-10-23T12:00:00Z')
  it.each([
    ['2026-10-23T11:59:30Z', 'just now'],
    ['2026-10-23T11:59:00Z', '1 minute ago'],
    ['2026-10-23T11:15:00Z', '45 minutes ago'],
    ['2026-10-23T09:00:00Z', '3 hours ago'],
    ['2026-10-21T12:00:00Z', '2 days ago'],
    ['2026-10-23T12:00:05Z', 'just now'],
  ])('%s', (iso, want) => expect(ago(iso, now)).toBe(want))
})
