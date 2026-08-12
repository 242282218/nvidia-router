import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { proxyPoolApi } from './api'
import ProxyPoolView from './ProxyPoolView.vue'

vi.mock('./api', () => ({
  proxyPoolApi: {
    get: vi.fn(),
    update: vi.fn(),
    status: vi.fn(),
  },
}))

const settings = {
  enabled: true,
  proxy_url: 'http://proxy-pool:8080',
  auth_configured: true,
  source: 'environment' as const,
}

const emptyStatus = {
  total_size: 0,
  healthy_size: 0,
  proxies: [],
  last_fetch_at: '',
  last_success_at: '',
  last_error_code: '',
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(proxyPoolApi.get).mockResolvedValue({ data: settings })
  vi.mocked(proxyPoolApi.update).mockResolvedValue({ data: { ...settings, source: 'database' } })
  vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: emptyStatus })
})

describe('ProxyPoolView', () => {
  it('shows the proxy address, enabled state, and redacted auth status', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    expect(wrapper.get('h1').text()).toContain('代理池')
    expect((wrapper.get('[data-testid="proxy-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="proxy-url"]').element as HTMLInputElement).value).toBe(settings.proxy_url)
    expect(wrapper.get('[data-testid="proxy-auth-status"]').text()).toContain('已配置')
    expect(wrapper.text()).not.toContain('proxy-secret')
  })

  it('saves a disabled proxy configuration and can clear the key without displaying it', async () => {
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    await wrapper.get('[data-testid="proxy-enabled"]').setValue(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(proxyPoolApi.update).toHaveBeenCalledWith({
      enabled: false,
      proxy_url: settings.proxy_url,
      auth_key: '',
    }, expect.any(AbortSignal))

    await wrapper.get('[data-testid="proxy-clear-auth"]').trigger('click')
    await flushPromises()
    expect(proxyPoolApi.update).toHaveBeenLastCalledWith({
      enabled: false,
      proxy_url: settings.proxy_url,
      auth_key: '',
      clear_auth_key: true,
    }, expect.any(AbortSignal))
  })

  it('shows live pool status: healthy counts and per-proxy quality', async () => {
    vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: {
      total_size: 2,
      healthy_size: 1,
      proxies: [
        { address: '10.0.0.1:8080', latency_ewma_ms: 150, remaining_seconds: 95, healthy: true, ejected: false, success_count: 8, failure_count: 0, http_fail_count: 0 },
        { address: '10.0.0.2:8080', latency_ewma_ms: 900, remaining_seconds: 30, healthy: false, ejected: true, success_count: 1, failure_count: 4, http_fail_count: 2 },
      ],
      last_fetch_at: '2026-08-12T04:00:00Z',
      last_success_at: '2026-08-12T04:00:00Z',
      last_error_code: '',
    } })
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('健康 1 / 2')
    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('10.0.0.1:8080')
    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('10.0.0.2:8080')
    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('150 ms')
    // The throttled exit shows its consecutive HTTP-failure pattern.
    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('限流信号 ×2')
  })

  it('surfaces collector health: last fetch time and a provider error hint', async () => {
    vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: {
      total_size: 1,
      healthy_size: 1,
      proxies: [],
      last_fetch_at: '2026-08-12T04:00:00Z',
      last_success_at: '2026-08-12T03:58:00Z',
      last_error_code: '403',
    } })
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    const panel = wrapper.get('[data-testid="proxy-status-panel"]')
    expect(panel.text()).toContain('上次采集')
    expect(panel.text()).toContain('上游异常（403）')
    // The raw upstream error (which embeds credentials) is never rendered.
    expect(panel.text()).not.toContain('http')
    expect(panel.text()).not.toContain('?')
  })

  it('shows a healthy collector badge when the last fetch succeeded', async () => {
    vi.mocked(proxyPoolApi.status).mockResolvedValue({ data: {
      total_size: 0,
      healthy_size: 0,
      proxies: [],
      last_fetch_at: '2026-08-12T04:00:00Z',
      last_success_at: '2026-08-12T04:00:00Z',
      last_error_code: '',
    } })
    const wrapper = mount(ProxyPoolView)
    await flushPromises()

    expect(wrapper.get('[data-testid="proxy-status-panel"]').text()).toContain('采集正常')
  })
})
