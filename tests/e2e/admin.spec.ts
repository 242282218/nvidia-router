import { expect, test, type Page } from '@playwright/test'

const initialPassword = 'e2e-initial-admin-password'
const newPassword = 'e2e-admin-password-2026'

async function login(page: Page, password = newPassword): Promise<void> {
  await page.goto('/admin/login')
  await page.locator('input[name="username"]').fill('admin')
  await page.locator('input[name="password"]').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
}

test.describe('administrator authentication', () => {
  test('forces the initial password change and supports logout/login', async ({ page }) => {
    await page.goto('/admin/login')
    await page.locator('input[name="username"]').fill('admin')
    await page.locator('input[name="password"]').fill(initialPassword)
    await page.getByRole('button', { name: '登录' }).click()
    await expect(page).toHaveURL(/\/admin\/change-password$/)

    const blockedProxy = await page.request.post('/v1/chat/completions', {
      data: { model: 'unused', messages: [{ role: 'user', content: 'blocked' }] },
      headers: { Authorization: 'Bearer e2e-access-key' },
    })
    expect(blockedProxy.status()).toBe(403)

    await page.locator('input[name="current-password"]').fill(initialPassword)
    await page.locator('input[name="new-password"]').fill(newPassword)
    await page.getByRole('button', { name: '修改密码' }).click()
    await expect(page).toHaveURL(/\/admin\/$/)
    await expect(page.getByTestId('nav-nvidia-keys')).toBeVisible()

    await page.getByTestId('logout').click()
    await expect(page).toHaveURL(/\/admin\/login$/)
    await login(page)
    await expect(page).toHaveURL(/\/admin\/$/)
    await expect(page.getByRole('main').getByText('管理端已登录。')).toBeVisible()
  })
})
