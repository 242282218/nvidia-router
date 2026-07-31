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

test.describe('runtime and responsive management UI', () => {
  test('shows runtime summary and saves a setting', async ({ page }) => {
    await login(page)
    await page.goto('/admin/runtime')
    await expect(page.getByTestId('runtime-key-counts')).toBeVisible()
    await expect(page.getByTestId('runtime-active')).toBeVisible()
    await page.getByTestId('queue-capacity').fill('120')
    await page.getByTestId('runtime-settings-form').getByRole('button', { name: '保存设置' }).click()
    await expect(page.getByTestId('queue-capacity')).toHaveValue('120')
    await expect(page.getByTestId('runtime-settings-form')).toBeVisible()
  })

  test('uses mobile cards and exposes desktop-only advanced-operation hints', async ({ page }) => {
    await login(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/admin/nvidia-keys')
    await expect(page.getByTestId('mobile-batch-hint')).toBeVisible()
    await expect(page.getByTestId('key-cards')).toBeVisible()
    await expect(page.getByTestId('key-table')).toBeHidden()
    const keyInput = page.locator('input[name="nvidia-key"]')
    await expect(keyInput).toBeVisible()
    await keyInput.fill('nvapi-fixture-not-a-real-key-123456789')
    await page.getByTestId('single-import-form').getByRole('button', { name: '导入' }).click()
    await expect(page.getByTestId('key-cards').locator('article').first()).toBeVisible()
    await page.getByTestId('key-card-test').first().click()
    await expect(page.getByTestId('key-test-results')).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()
    await page.getByTestId('key-card-toggle').first().click()
    await expect(page.getByTestId('key-cards')).toContainText('停用')
    await page.getByTestId('key-card-toggle').first().click()

    await page.goto('/admin/models')
    await expect(page.getByTestId('mobile-model-hint')).toBeVisible()
    await expect(page.getByTestId('model-cards')).toBeVisible()
    await expect(page.getByTestId('model-table')).toBeHidden()

    await page.goto('/admin/access-keys')
    await page.getByTestId('open-create-access-key').click()
    await page.getByTestId('access-key-name').fill('mobile-e2e-client')
    await page.getByTestId('create-access-key-form').getByRole('button', { name: '创建' }).click()
    const mobilePlaintext = await page.getByTestId('created-access-key').textContent()
    expect(mobilePlaintext).toMatch(/^nvr_/)
    await page.getByTestId('close-created-access-key').click()
    await expect(page.getByTestId('created-access-key')).toHaveCount(0)
    await expect(page.locator('body')).not.toContainText(mobilePlaintext ?? '')
    await expect(page.getByTestId('access-key-cards')).toBeVisible()
    await expect(page.getByTestId('access-key-table')).toBeHidden()
    page.once('dialog', (dialog) => void dialog.accept())
    const mobileAccessKeyCard = page.getByTestId('access-key-cards').locator('article').filter({ hasText: 'mobile-e2e-client' })
    await mobileAccessKeyCard.getByRole('button', { name: '撤销' }).click()
    await expect(mobileAccessKeyCard).toContainText('已撤销')
  })
})
