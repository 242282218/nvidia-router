import { expect, test, type Page } from '@playwright/test'

const validKey = 'fixture-second-valid-key-123456789'
const secondValidKey = 'nvapi-fixture-not-a-real-key-123456789'
const invalidKey = 'invalid-key-value-that-is-long-enough'

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

test.describe('management resources', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
  })

  test('imports one key, reports partial batch results, and never exposes plaintext', async ({ page }) => {
    await page.goto('/admin/nvidia-keys')
    await expect(page.getByTestId('single-import-form')).toBeVisible()

    await page.locator('input[name="nvidia-key"]').fill(validKey)
    await page.getByTestId('single-import-form').getByRole('button', { name: '导入' }).click()
    await expect(page.getByText('imported')).toBeVisible()
    await expect(page.getByTestId('key-table')).toBeVisible()
    await expect(page.getByTestId('key-cards')).toBeHidden()
    await expect(page.getByTestId('key-table')).toContainText('fixture-…6789')

    await page.getByTestId('open-batch-import').click()
    await page.locator('textarea[name="batch-keys"]').fill(`${invalidKey}\n${secondValidKey}`)
    await page.getByRole('dialog').getByRole('button', { name: '导入' }).click()
    await expect(page.getByRole('dialog')).toContainText('invalid_credential')
    await expect(page.getByRole('dialog')).toContainText('imported')
    await expect(page.getByRole('dialog')).toContainText('nvapi-fi...6789')
    await expect(page.locator('body')).not.toContainText(validKey)
    await expect(page.locator('body')).not.toContainText(secondValidKey)
    await expect(page.locator('textarea[name="batch-keys"]').first()).toHaveValue('')
  })

  test('supports model discovery, selection, key toggle, and test-all', async ({ page }) => {
    await page.goto('/admin/nvidia-keys')
    await page.locator('input[name="nvidia-key"]').fill(validKey)
    await page.getByTestId('single-import-form').getByRole('button', { name: '导入' }).click()
    await expect(page.getByTestId('key-table')).toBeVisible()
    await expect(page.getByTestId('key-cards')).toBeHidden()
    await expect(page.getByTestId('key-table')).toContainText('fixture-…6789')
    await page.getByTestId('test-all-keys').click()
    await expect(page.getByTestId('key-test-results')).toContainText('valid')
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()

    const toggle = page.getByTestId('key-table-toggle-1')
    await toggle.click()
    await expect(page.getByTestId('key-table')).toContainText('停用')
    await toggle.click()
    await expect(page.getByTestId('key-table')).toContainText('启用')

    await page.goto('/admin/models')
    await page.getByTestId('discover-models').click()
    await expect(page.getByTestId('candidate-meta/llama-3.1-8b-instruct')).toBeVisible()
    await page.getByTestId('candidate-meta/llama-3.1-8b-instruct').check()
    await page.getByTestId('save-candidates').click()
    await expect(page.getByTestId('model-table')).toBeVisible()
    await expect(page.getByTestId('model-cards')).toBeHidden()
    await expect(page.getByTestId('model-table')).toContainText('meta/llama-3.1-8b-instruct')
  })

  test('creates and revokes an access key with one-time display', async ({ page }) => {
    await page.getByTestId('nav-access-keys').click()
    await page.getByTestId('open-create-access-key').click()
    await page.getByTestId('access-key-name').fill('e2e-client')
    await page.getByTestId('create-access-key-form').getByRole('button', { name: '创建' }).click()
    const created = page.getByTestId('created-access-key')
    await expect(created).toHaveText(/nvr_[A-Za-z0-9_-]+/)
    const plaintext = await created.textContent()
    expect(plaintext).toMatch(/^nvr_/)
    await page.getByTestId('close-created-access-key').click()
    await expect(page.getByTestId('created-access-key')).toHaveCount(0)
    await expect(page.locator('body')).not.toContainText(plaintext ?? '')
    await expect(page.getByTestId('access-key-table')).toBeVisible()
    await expect(page.getByTestId('access-key-cards')).toBeHidden()
    const desktopAccessKeyRow = page.getByTestId('access-key-table').getByRole('row').filter({ hasText: 'e2e-client' })
    await expect(desktopAccessKeyRow).toBeVisible()
    page.once('dialog', (dialog) => void dialog.accept())
    await desktopAccessKeyRow.getByRole('button', { name: '撤销' }).click()
    await expect(desktopAccessKeyRow).toContainText('已撤销')
  })
})
