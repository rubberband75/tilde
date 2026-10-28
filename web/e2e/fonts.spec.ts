import { expect, test } from '@playwright/test'

// The bundled Ubuntu font (D56): served as a font from Tilde's own origin
// before anyone logs in, loadable under the CSP, and the fallbacks take
// the same space as Ubuntu so nothing moves when it arrives.

// Chromium on Linux hints web fonts to whole-pixel letter widths, which
// makes size-adjust move in steps (101.77% measured 9% wider). Windows and
// macOS, where the Arial fallback is for, place letters by fractions of a
// pixel, as Chromium does without hinting.
test.use({ launchOptions: { args: ['--font-render-hinting=none'] } })
test('the bundled font loads, and its fallbacks take its space', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('button', { name: /log in/i })).toBeVisible()

  const url = await page.evaluate(() => {
    for (const sheet of Array.from(document.styleSheets)) {
      for (const rule of Array.from(sheet.cssRules)) {
        if (!(rule instanceof CSSFontFaceRule) || !rule.style.getPropertyValue('font-family').includes('Tilde UI'))
          continue
        const m = /url\("?([^")]+)"?\)/.exec(rule.style.getPropertyValue('src'))
        if (m) return new URL(m[1]!, sheet.href ?? location.href).href
      }
    }
    return null
  })
  expect(url).toMatch(/\/assets\/tilde-ui-[\w-]+\.woff2$/)
  const res = await page.request.get(url!)
  expect(res.headers()['content-type']).toBe('font/woff2')
  expect(res.headers()['cache-control']).toContain('immutable')

  // Loaded by name, so an installed Ubuntu on this machine cannot stand in.
  const status = await page.evaluate(async (u) => {
    const face = new FontFace('Bundled Ubuntu', `url(${u})`)
    // add() is missing from this TypeScript DOM library.
    ;(document.fonts as unknown as Set<FontFace>).add(face)
    await face.load()
    return face.status
  }, url!)
  expect(status).toBe('loaded')

  const widths = await page.evaluate(async () => {
    const sample = 'Home Desktop Documents Downloads Music Pictures Videos Trash'
    const measure = async (family: string) => {
      try {
        if ((await document.fonts.load(`13.333px "${family}"`)).length === 0) return null
      } catch {
        return null // not installed here
      }
      const span = document.createElement('span')
      span.style.font = `13.333px "${family}"`
      span.style.whiteSpace = 'nowrap'
      span.textContent = sample
      document.body.append(span)
      const w = span.getBoundingClientRect().width
      span.remove()
      return w
    }
    return {
      ubuntu: await measure('Bundled Ubuntu'),
      arial: await measure('Tilde UI Fallback'),
      dejavu: await measure('Tilde UI Fallback DejaVu'),
    }
  })
  expect(widths.ubuntu).toBeGreaterThan(0)
  let measured = 0
  for (const w of [widths.arial, widths.dejavu]) {
    if (w === null) continue
    measured++
    expect(Math.abs(w / widths.ubuntu! - 1)).toBeLessThan(0.04)
  }
  expect(measured, 'neither Liberation Sans / Arial nor DejaVu Sans is installed here').toBeGreaterThan(0)
})
