import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { proxyPoolApi } from './api'
import ProxyPoolView from './ProxyPoolView.vue'

vi.mock('./api', () => ({
  proxyPoolApi: {
    get: vi.fn(),
    update: vi.fn(),
  },
}))

const settings = {
  enabled: true,
  proxy_url: 'http://proxy-pool:8080',
  auth_configured: true,
  source: 'environment' as const,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(proxyPoolApi.get).mockResolvedValue({ data: settings })
  vi.mocked(proxyPoolApi.update).mockResolvedValue({ data: { ...settings, source: 'database' } })
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
})
