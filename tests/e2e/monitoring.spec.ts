import { expect, test, type Page } from '@playwright/test'

async function login(page: Page): Promise<void> {
  await page.goto('/admin/login')
  await page.locator('input[name="username"]').fill('admin')
  await page.locator('input[name="password"]').fill('e2e-admin-password-2026')
  await page.getByRole('button', { name: '登录' }).click()
  try {
    await expect(page).toHaveURL(/\/admin\/$/, { timeout: 2_000 })
    return
  } catch {
    await page.locator('input[name="password"]').fill('admin')
    await page.getByRole('button', { name: '登录' }).click()
    await expect(page).toHaveURL(/\/admin\/change-password$/)
    await page.locator('input[name="current-password"]').fill('admin')
    await page.locator('input[name="new-password"]').fill('e2e-admin-password-2026')
    await page.getByRole('button', { name: '修改密码' }).click()
  }
  await expect(page).toHaveURL(/\/admin\/$/)
}

test.describe('monitoring dashboard after the observability split', () => {
  test('shows the split pages in nav and renders the monitoring dashboard', async ({ page }) => {
    await login(page)

    // The split pages are first-class nav entries in the 系统观测 group.
    await expect(page.getByTestId('nav-runtime')).toBeVisible()
    await expect(page.getByTestId('nav-monitoring')).toBeVisible()

    // Legacy bookmarks migrate to the new standalone pages.
    await page.goto('/admin/system?tab=statistics')
    await expect(page).toHaveURL(/\/admin\/monitoring$/)
    await expect(page.getByTestId('monitoring-kpi-row')).toBeVisible()
    await expect(page.getByTestId('monitoring-filter-panel')).toBeVisible()
    await expect(page.getByTestId('traffic-chart')).toBeVisible()
    await expect(page.getByTestId('health-timeline')).toBeVisible()
    await expect(page.getByTestId('monitoring-outcome-list')).toBeVisible()
    await expect(page.getByTestId('monitoring-log-table')).toBeVisible()

    // The range control lives in the filter panel now.
    await page.getByTestId('range-7d').click()
    await expect(page).toHaveURL(/\/admin\/monitoring$/)
    await expect(page.getByTestId('range-7d')).toHaveAttribute('aria-pressed', 'true')
  })

  test('shows the runtime dashboard with channel health after the split', async ({ page }) => {
    await login(page)
    await page.goto('/admin/system?tab=runtime')
    await expect(page).toHaveURL(/\/admin\/runtime$/)
    await expect(page.getByTestId('runtime-kpi-row')).toBeVisible()
    await expect(page.getByTestId('runtime-channel-health')).toBeVisible()
    await expect(page.getByTestId('runtime-key-pool-row').first()).toBeVisible()
    await expect(page.getByTestId('runtime-settings-anchor')).toBeVisible()
  })
})
