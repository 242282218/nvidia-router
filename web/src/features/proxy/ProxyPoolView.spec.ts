import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { proxyPoolApi } from './api'
import ProxyPoolView from './ProxyPoolView.vue'
import type { ProxyPoolSettings } from './types'

vi.mock('./api', () => ({
  proxyPoolApi: {
    get: vi.fn(),
    update: vi.fn(),
    status: vi.fn(),
    refresh: vi.fn(),
  },
}))

// 视图通过 ?collect=1 深链触发立即采集，需要路由上下文
async function mountView(query?: Record<string, string>) {
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [{ path: '/proxy-pool', component: ProxyPoolView }],
  })
  await router.push({ path: '/proxy-pool', query })
  await router.isReady()
  return mount(ProxyPoolView, { global: { plugins: [router] } })
}

const settings = {
  enabled: true,
  proxy_url: '',
  auth_configured: true,
  source: 'environment' as const,
  mode: 'built-in' as const,
  upstream_configured: true,
  upstream_endpoint: 'https://api.example.test/tools/XApi.ashx',
  validation_url: 'https://validate.example.test/health',
  validation_status: 204,
  collector_interval: '11s',
  proxy_ttl: '33s',
  expected_qty: 7,
  concurrency: 4,
  max_latency: '2s',
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
    const wrapper = await mountView()
    await flushPromises()

    expect(wrapper.get('h1').text()).toContain('代理池')
    expect((wrapper.get('[data-testid="proxy-enabled"]').element as HTMLInputElement).checked).toBe(true)
    const upstreamInput = wrapper.get('[data-testid="proxy-upstream-url"]')
    expect((upstreamInput.element as HTMLInputElement).type).toBe('password')
    expect(wrapper.get('[data-testid="proxy-upstream-summary"]').text()).toContain('已配置')
    expect((wrapper.get('[data-testid="proxy-validation-url"]').element as HTMLInputElement).value).toBe('https://validate.example.test/health')
    expect((wrapper.get('[data-testid="proxy-validation-status"]').element as HTMLInputElement).value).toBe('204')
    expect((wrapper.get('[data-testid="proxy-concurrency"]').element as HTMLInputElement).value).toBe('4')
    expect((wrapper.get('[data-testid="proxy-max-latency"]').element as HTMLInputElement).value).toBe('2s')
    expect(wrapper.get('#proxy-upstream-help').text()).toContain('管理端可修改')
    expect(wrapper.text()).not.toContain('apikey=')
    expect(wrapper.text()).toContain('运行正常')
  })

  it('saves collector settings and does not send a fixed proxy credential', async () => {
    const wrapper = await mountView()
    await flushPromises()

    await wrapper.get('[data-testid="proxy-interval"]').setValue('10s')
    await wrapper.get('[data-testid="proxy-ttl"]').setValue('90s')
    await wrapper.get('[data-testid="proxy-upstream-url"]').setValue('https://new.example.test/tools/XApi.ashx?apikey=fixture&sign=fixture')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(proxyPoolApi.update).toHaveBeenCalledWith(expect.objectContaining({
      enabled: true,
      interval: '10s',
      proxy_ttl: '90s',
      validation_url: 'https://validate.example.test/health',
      validation_status: 204,
      expected_qty: 7,
      concurrency: 4,
      max_latency: '2s',
      upstream_url: 'https://new.example.test/tools/XApi.ashx?apikey=fixture&sign=fixture',
    }), expect.any(AbortSignal))
    const updateMock = vi.mocked(proxyPoolApi.update)
    expect(updateMock.mock.calls[0]?.[0]).not.toHaveProperty('auth_key')
    expect((wrapper.get('[data-testid="proxy-upstream-url"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('配置已保存')
  })

  it('saves successfully when max latency is absent from the snapshot', async () => {
    const settingsWithoutMaxLatency = { ...settings } as Omit<typeof settings, 'max_latency'> & { max_latency?: string }
    delete settingsWithoutMaxLatency.max_latency
    vi.mocked(proxyPoolApi.get).mockResolvedValueOnce({ data: settingsWithoutMaxLatency })
    const wrapper = await mountView()
    await flushPromises()

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(proxyPoolApi.update).toHaveBeenCalledWith(expect.objectContaining({ max_latency: '' }), expect.any(AbortSignal))
  })

  it('saves defaults when collector fields are absent from a disabled snapshot', async () => {
    const settingsWithoutCollectorFields = { ...settings }
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).validation_url
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).validation_status
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).collector_interval
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).proxy_ttl
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).expected_qty
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).concurrency
    delete (settingsWithoutCollectorFields as Partial<typeof settings>).max_latency
    settingsWithoutCollectorFields.enabled = false
    settingsWithoutCollectorFields.upstream_configured = false
    vi.mocked(proxyPoolApi.get).mockResolvedValueOnce({ data: settingsWithoutCollectorFields as unknown as ProxyPoolSettings })
    const wrapper = await mountView()
    await flushPromises()

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(proxyPoolApi.update).toHaveBeenCalledWith(expect.objectContaining({
      enabled: false,
      validation_url: '',
      validation_status: 404,
      interval: '5s',
      proxy_ttl: '120s',
      expected_qty: 2,
      concurrency: 2,
      max_latency: '',
    }), expect.any(AbortSignal))
  })

  it('runs one immediate collection and refreshes status', async () => {
    const wrapper = await mountView()
    await flushPromises()

    await wrapper.get('[data-testid="proxy-refresh-now"]').trigger('click')
    await flushPromises()

    expect(proxyPoolApi.refresh).toHaveBeenCalledOnce()
    expect(proxyPoolApi.status).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('已完成一轮采集与验证')
  })

  it('triggers immediate collection from the ?collect=1 deep link', async () => {
    const wrapper = await mountView({ collect: '1' })
    await flushPromises()

    expect(proxyPoolApi.refresh).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('已完成一轮采集与验证')
  })

  it('renders pool quality rows and an empty-pool recovery message', async () => {
    const wrapper = await mountView()
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
    const wrapper = await mountView()
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
    const wrapper = await mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="proxy-settings-load-error"]').text()).toContain('代理池配置加载失败')
    expect(wrapper.find('[data-testid="proxy-enabled"]').exists()).toBe(false)
    await wrapper.get('[data-testid="proxy-settings-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-settings-load-error"]').exists()).toBe(false)
  })
})
