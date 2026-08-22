// 响应式 0px 横向溢出探针（设计文档 §9.6 验收线）。
// 用法：先启动带隔离数据库的 e2e harness（tests/e2e/harness），然后
//   node scripts/test/web_overflow_probe.mjs [baseURL]
// 默认 baseURL http://127.0.0.1:3756。登录走 e2e 既有口令，仅本地隔离库有效。
// Playwright 安装在 web 工作区（ESM 不读 NODE_PATH，用 createRequire 解析）。
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const require = createRequire(fileURLToPath(new URL('../../web/package.json', import.meta.url)))
const { chromium } = require('@playwright/test')

const baseURL = process.argv[2] ?? process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:3756'

// 视口矩阵：320/390/768/1280/1440/1699 + 200% 缩放（等效 720 宽 + DSF2）。
const viewports = [
  { name: '320', width: 320, height: 800, scale: 1 },
  { name: '390', width: 390, height: 844, scale: 1 },
  { name: '768', width: 768, height: 1024, scale: 1 },
  { name: '1280', width: 1280, height: 900, scale: 1 },
  { name: '1440', width: 1440, height: 900, scale: 1 },
  { name: '1699', width: 1699, height: 942, scale: 1 },
  { name: '720@200%', width: 720, height: 900, scale: 2 },
]

const pages = [
  '/admin/runtime',
  '/admin/monitoring',
  '/admin/system',
]

const browser = await chromium.launch()
let failures = 0

try {
  for (const viewport of viewports) {
    const context = await browser.newContext({
      viewport: { width: viewport.width, height: viewport.height },
      deviceScaleFactor: viewport.scale,
    })
    const page = await context.newPage()
    await page.goto(`${baseURL}/admin/login`)
    await page.locator('input[name="username"]').fill('admin')
    await page.locator('input[name="password"]').fill('e2e-admin-password-2026')
    await page.getByRole('button', { name: '登录' }).click()
    try {
      await page.waitForURL(/\/admin\/$/, { timeout: 2_000 })
    } catch {
      // 全新隔离库：初始口令 e2e-initial-admin-password（harness main.go 注入）
      // 且强制改密，改成与 e2e 用例一致的口令后进入后台。
      await page.locator('input[name="password"]').fill('e2e-initial-admin-password')
      await page.getByRole('button', { name: '登录' }).click()
      await page.waitForURL(/\/admin\/change-password$/)
      await page.locator('input[name="current-password"]').fill('e2e-initial-admin-password')
      await page.locator('input[name="new-password"]').fill('e2e-admin-password-2026')
      await page.getByRole('button', { name: '修改密码' }).click()
      await page.waitForURL(/\/admin\/$/)
    }

    for (const path of pages) {
      await page.goto(`${baseURL}${path}`, { waitUntil: 'load' })
      // SSE 页面（/admin/system 实时流）永远到不了 networkidle；
      // 用固定稳定窗等轮询/图表首帧渲染完成。
      await page.waitForTimeout(1_200)
      const overflow = await page.evaluate(() => {
        const doc = document.documentElement
        const body = document.body
        const docOverflow = doc ? doc.scrollWidth - doc.clientWidth : 0
        const bodyOverflow = body ? body.scrollWidth - body.clientWidth : 0
        return Math.max(docOverflow, bodyOverflow)
      })
      const status = overflow > 0 ? `OVERFLOW ${overflow}px` : 'OK'
      if (overflow > 0) failures += 1
      console.log(`${viewport.name.padEnd(10)} ${path.padEnd(20)} ${status}`)
    }
    await context.close()
  }
} finally {
  await browser.close()
}

if (failures > 0) {
  console.error(`\n${failures} page/viewport combination(s) have horizontal overflow.`)
  process.exit(1)
}
console.log('\nAll pages are free of horizontal overflow at every probed viewport.')
