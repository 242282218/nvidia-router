import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { proxyPoolApi } from './api'
import ProxyPoolView from './ProxyPoolView.vue'

vi.mock('./api', () => ({
  proxyPoolApi: {
    get: vi.fn(),
    update: vi.fn(),
    status: vi.fn(),
    refresh: vi.fn(),
  },
}))

const settings = {
  enabled: true,
  proxy_url: '',
  auth_configured: true,
  source: 'environment' as const,
  mode: 'built-in' as const,
  upstream_configured: true,
  upstream_endpoint: 'https://api.example.test/tools/XApi.ashx',
  collector_interval: '5s',
  proxy_ttl: '120s',
}

const emptyStatus = {
  configured: true,
  mode: 'built-in' as const,
  total_size: 2,
  healthy_size: 2,
  collector_enabled: true,
  last_success_at: '2026-08-14T08:00:00Z',
  proxies: [{ address: '203.0.113.10:8080', healthy: true, latency_ewma_ms: 42, quality_score: 91, remaining_seconds: 96 }],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(proxyPoolApi.get).mockResolvedValue({ data: settings })
  vi.mocked(proxyPoolApi.update).mockResolvedValue({ data: { ...settings, source: 'database' } })
  vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: emptyStatus })
  vi.mocked(proxyPoolApi.refresh).mockResolvedValue({ data: { message: 'ok' } })
})

describe('ProxyPoolView', () => {
  it('shows built-in collector configuration without exposing the XApi secret', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    expect(wrapper.get('h1').text()).toContain('代理池')
    expect((wrapper.get('[data-testid="proxy-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="proxy-upstream-url"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('#proxy-upstream-help').text()).toContain('保存后只显示主机和路径')
    expect(wrapper.text()).not.toContain('apikey=')
    expect(wrapper.text()).toContain('运行正常')
  })

  it('saves collector settings and does not send a fixed proxy credential', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    await wrapper.get('[data-testid="proxy-interval"]').setValue('10s')
    await wrapper.get('[data-testid="proxy-ttl"]').setValue('90s')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(proxyPoolApi.update).toHaveBeenCalledWith(expect.objectContaining({
      enabled: true,
      interval: '10s',
      proxy_ttl: '90s',
      expected_qty: 2,
      concurrency: 2,
    }), expect.any(AbortSignal))
    const updateMock = vi.mocked(proxyPoolApi.update)
    expect(updateMock.mock.calls[0]?.[0]).not.toHaveProperty('auth_key')
    expect(wrapper.text()).toContain('配置已保存')
  })

  it('runs one immediate collection and refreshes status', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    await wrapper.get('[data-testid="proxy-refresh-now"]').trigger('click')
    await flushPromises()

    expect(proxyPoolApi.refresh).toHaveBeenCalledOnce()
    expect(proxyPoolApi.status).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('已完成一轮采集与验证')
  })

  it('renders pool quality rows and an empty-pool recovery message', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()
    expect(wrapper.text()).toContain('203.0.113.10:8080')
    expect(wrapper.text()).toContain('42 ms')
    expect(wrapper.text()).toContain('91')

    vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: { ...emptyStatus, healthy_size: 0, proxies: [] } })
    await wrapper.get('section button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('暂无可用出口')
    expect(wrapper.text()).toContain('暂无已验证出口')
  })

  it('keeps a recoverable status error visible after malformed data', async () => {
    vi.mocked(proxyPoolApi.status)
      .mockResolvedValueOnce({ data: emptyStatus })
      .mockResolvedValueOnce({ data: { ...emptyStatus, healthy_size: -1 } })
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    await wrapper.get('section button').trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('代理池状态加载失败')
    expect(wrapper.text()).toContain('203.0.113.10:8080')
  })

  it('shows a persistent settings load error with retry', async () => {
    vi.mocked(proxyPoolApi.get)
      .mockRejectedValueOnce(new Error('backend unreachable'))
      .mockResolvedValueOnce({ data: settings })
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    expect(wrapper.get('[data-testid="proxy-settings-load-error"]').text()).toContain('代理池配置加载失败')
    expect(wrapper.find('[data-testid="proxy-enabled"]').exists()).toBe(false)
    await wrapper.get('[data-testid="proxy-settings-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-settings-load-error"]').exists()).toBe(false)
  })
})
