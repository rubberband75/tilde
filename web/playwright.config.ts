import { defineConfig, devices } from '@playwright/test'

// UI tests: a real browser against a real Tilde with a throwaway account.
// They log in, create files and end sessions, so run them only against a
// disposable machine: "make e2e-vm" runs them in the e2e VM after the Go
// suite (docs/development.md). By hand:
//   TILDE_URL=https://localhost:9443 TILDE_USER=tester TILDE_PASS=tester-pass npx playwright test
const baseURL = process.env.TILDE_URL

if (!baseURL || !process.env.TILDE_USER || !process.env.TILDE_PASS) {
  throw new Error('set TILDE_URL, TILDE_USER and TILDE_PASS (a throwaway account in a VM)')
}

export default defineConfig({
  testDir: './e2e',
  // One user, one worker: tests share it, and logging out ends it for all.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  timeout: 30_000,
  expect: { timeout: 5_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  outputDir: 'test-results',
  use: {
    baseURL,
    // The VM serves a self-signed certificate.
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } } }],
})
