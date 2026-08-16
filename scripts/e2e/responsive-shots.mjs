/* Responsive evidence capture for the UI refactor review (web P0#1/P0#2:
 * usable from 320px through 1440px, no content loss at 200% zoom).
 * One-shot script — not wired into CI. Run against the e2e harness:
 *   node scripts/e2e/responsive-shots.mjs */
import { mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
// Direct path import: this script lives outside the web workspace, and ESM
// resolution does not honour NODE_PATH the way the Playwright runner does.
import pw from '../../web/node_modules/@playwright/test/index.js'

const { chromium } = pw

const BASE_URL = process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:3756'
const INITIAL_PASSWORD = 'e2e-initial-admin-password'
const NEW_PASSWORD = 'e2e-admin-password-2026'
// fileURLToPath decodes the percent-encoded CJK segment in the repo path.
const OUT_DIR = fileURLToPath(new URL('../../web/test-results/responsive/', import.meta.url))
mkdirSync(OUT_DIR, { recursive: true })

const pages = [
  { path: '/admin/nvidia-keys', name: 'nvidia-keys' },
  { path: '/admin/statistics', name: 'statistics' },
  { path: '/admin/proxy-pool', name: 'proxy-pool' },
  { path: '/admin/audit', name: 'audit' },
]

const viewports = [
  { width: 320, height: 700, name: '320' },
  { width: 768, height: 900, name: '768' },
  { width: 1440, height: 900, name: '1440' },
]

async function login(page, password) {
  await page.goto(`${BASE_URL}/admin/login`)
  await page.locator('input[name="username"]').fill('admin')
  await page.locator('input[name="password"]').fill(password)
  await page.locator('button[type="submit"]').click()
  await page.waitForURL(/admin/, { timeout: 10_000 })
}

const browser = await chromium.launch()
try {
  {
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
    const page = await context.newPage()

    // Force the initial password change once, then log in with the new one.
    await login(page, INITIAL_PASSWORD)
    if (page.url().includes('change-password')) {
      await page.locator('input[name="current-password"]').fill(INITIAL_PASSWORD)
      await page.locator('input[name="new-password"]').fill(NEW_PASSWORD)
      await page.locator('button[type="submit"]').click()
      await page.waitForURL(/admin\/nvidia-keys|\/admin\/$/, { timeout: 10_000 })
    }
    await context.close()
  }

  for (const viewport of viewports) {
    const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } })
    const page = await context.newPage()
    await login(page, NEW_PASSWORD)
    for (const target of pages) {
      await page.goto(`${BASE_URL}${target.path}`)
      await page.waitForLoadState('networkidle')
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      await page.screenshot({ path: `${OUT_DIR}/${target.name}-${viewport.name}.png`, fullPage: false })
      console.log(`${target.name}-${viewport.name}: horizontal-overflow=${overflow}px`)
    }
    await context.close()
  }  // 200% zoom spot check (deviceScaleFactor 2 at a 720px window ≈ 1440 CSS px at 200%).
  const zoomContext = await browser.newContext({ viewport: { width: 720, height: 700 }, deviceScaleFactor: 2 })
  const page = await zoomContext.newPage()
  await login(page, NEW_PASSWORD)
  await page.goto(`${BASE_URL}/admin/nvidia-keys`)
  await page.waitForLoadState('networkidle')
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  await page.screenshot({ path: `${OUT_DIR}/nvidia-keys-200pct.png` })
  console.log(`nvidia-keys-200pct: horizontal-overflow=${overflow}px`)
  await zoomContext.close()
} finally {
  await browser.close()
}
