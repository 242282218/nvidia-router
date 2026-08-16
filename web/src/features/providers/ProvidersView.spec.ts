import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { providersApi } from './api'
import ProvidersView from './ProvidersView.vue'

vi.mock('./api', () => ({
  providersApi: {
    list: vi.fn(),
    create: vi.fn(),
    setEnabled: vi.fn(),
  },
}))

const provider = {
  id: 3,
  name: 'siliconflow',
  base_url: 'https://api.siliconflow.cn/v1',
  display_prefix: 'sk-ab',
  display_suffix: 'cd',
  enabled: true,
  created_at: '2026-08-10T08:00:00Z',
  updated_at: '2026-08-10T08:00:00Z',
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(providersApi.list).mockResolvedValue({ data: [provider] })
})

describe('ProvidersView', () => {
  it('renders the provider list with masked credentials and status', async () => {
    const wrapper = mount(ProvidersView)
    await flushPromises()

    expect(wrapper.text()).toContain('siliconflow')
    expect(wrapper.text()).toContain('sk-ab…cd')
    expect(wrapper.text()).toContain('启用')
    // The full key must never be reconstructable from the page.
    expect(wrapper.text()).not.toContain('secret-key-material')
  })

  it('shows the empty state with guidance when no providers exist', async () => {
    vi.mocked(providersApi.list).mockResolvedValue({ data: [] })
    const wrapper = mount(ProvidersView)
    await flushPromises()

    expect(wrapper.text()).toContain('尚未配置 OpenAI 兼容提供商')
    expect(wrapper.text()).toContain('NVIDIA 作为内置提供商始终可用')
  })

  it('keeps a recoverable load error with retry', async () => {
    vi.mocked(providersApi.list)
      .mockRejectedValueOnce(new Error('backend unreachable'))
      .mockResolvedValueOnce({ data: [provider] })
    const wrapper = mount(ProvidersView)
    await flushPromises()

    const panel = wrapper.get('[role="alert"]')
    expect(panel.text()).toContain('提供商列表加载失败')
    const retry = panel.get('button')
    await retry.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('siliconflow')
  })

  it('creates a provider through the dialog and requires a valid base URL', async () => {
    vi.mocked(providersApi.create).mockResolvedValue({ data: { ...provider, id: 4 } })
    const wrapper = mount(ProvidersView)
    await flushPromises()

    await wrapper.get('[data-testid="open-create-provider"]').trigger('click')
    const dialog = wrapper.get('[role="dialog"]')
    expect(dialog.attributes('aria-labelledby')).toBe('create-provider-heading')

    await wrapper.get('[data-testid="provider-name"]').setValue('siliconflow')
    await wrapper.get('[data-testid="provider-base-url"]').setValue('not-a-url')
    await wrapper.get('[data-testid="provider-key"]').setValue('secret-key-material')
    await wrapper.get('[data-testid="create-provider-form"]').trigger('submit')
    await flushPromises()
    expect(providersApi.create).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Base URL 必须是有效的 HTTP 或 HTTPS 地址')

    await wrapper.get('[data-testid="provider-base-url"]').setValue('https://api.siliconflow.cn/v1')
    await wrapper.get('[data-testid="create-provider-form"]').trigger('submit')
    await flushPromises()
    expect(providersApi.create).toHaveBeenCalledWith('siliconflow', 'https://api.siliconflow.cn/v1', 'secret-key-material')
  })

  it('toggles a provider and reports the outcome via toast', async () => {
    vi.mocked(providersApi.setEnabled).mockResolvedValue({ id: 3, enabled: false })
    const wrapper = mount(ProvidersView)
    await flushPromises()

    await wrapper.get('[data-testid="toggle-provider-3"]').trigger('click')
    await flushPromises()
    // The fixture provider is enabled, so toggling requests disabled.
    expect(providersApi.setEnabled).toHaveBeenCalledWith(3, false)
  })
})
