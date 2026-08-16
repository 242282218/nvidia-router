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

  it('keeps unsupported providers read-only', async () => {
    const wrapper = mount(ProvidersView)
    await flushPromises()

    expect(wrapper.text()).toContain('当前运行时暂不支持非 NVIDIA 提供商')
    expect(wrapper.get('[data-testid="open-create-provider"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="toggle-provider-3"]').attributes('disabled')).toBeDefined()
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

  it('does not expose create or toggle actions for unsupported providers', async () => {
    const wrapper = mount(ProvidersView)
    await flushPromises()

    expect(wrapper.get('[data-testid="open-create-provider"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="toggle-provider-3"]').attributes('disabled')).toBeDefined()
    expect(providersApi.create).not.toHaveBeenCalled()
    expect(providersApi.setEnabled).not.toHaveBeenCalled()
  })
})
