import { createRequire } from 'node:module'
import { delimiter } from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineConfig, devices } from '@playwright/test'

// Specs are kept at repository level, while Playwright is installed in the
// web workspace. Add that workspace to Node's package lookup paths so the
// package import remains portable in local runs and CI.
process.env.NODE_PATH = [
  fileURLToPath(new URL('./node_modules', import.meta.url)),
  process.env.NODE_PATH,
].filter(Boolean).join(delimiter)
const moduleLoader = createRequire(import.meta.url)('node:module') as { Module: { _initPaths: () => void } }
moduleLoader.Module._initPaths()

export default defineConfig({
  testDir: '../tests/e2e',
  testMatch: '*.spec.ts',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI
    ? [['github'], ['html', { outputFolder: 'playwright-report', open: 'never' }]]
    : 'list',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:3756',
    headless: true,
    trace: 'off',
    screenshot: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
