import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ObservabilityView from './ObservabilityView.vue'

vi.mock('../runtime/api', () => ({
  runtimeApi: {
    getSummary: vi.fn().mockResolvedValue({
      data: {
        keys: { total: 10, enabled: 8, disabled: 2, auth_invalid: 0, cooling_down: 0, ready: 8 },
        active: 1,
        queue: { length: 0, capacity: 100 },
        shutting_down: false,
      },
    }),
    getSettings: vi.fn().mockResolvedValue({
      data: {
        queue_capacity: 100,
        queue_wait_timeout_ms: 60000,
        connect_timeout_ms: 10000,
        first_byte_timeout_ms: 60000,
        nonstream_total_timeout_ms: 300000,
        shutdown_grace_ms: 60000,
      },
    }),
  },
}))

vi.mock('../statistics/api', () => ({
  statisticsApi: {
    getSummary: vi.fn().mockResolvedValue({
      data: {
        range: '24h',
        from: '',
        to: '',
        summary: {
          request_count: 100,
          success_count: 98,
          failure_count: 2,
          success_rate: 98,
          average_duration_ms: 150,
          average_first_byte_ms: 50,
          average_first_token_ms: 60,
          average_queue_ms: 5,
          total_attempts: 102,
          prompt_tokens: 10000,
          completion_tokens: 5000,
        },
        series: [],
      },
    }),
    getLogs: vi.fn().mockResolvedValue({
      data: { items: [], page: 1, page_size: 50, total: 0, has_more: false },
    }),
    getCosts: vi.fn().mockResolvedValue({ data: [] }),
  },
}))

vi.mock('../audit/api', () => ({
  auditApi: {
    list: vi.fn().mockResolvedValue({
      data: { items: [], total: 0, has_more: false },
    }),
  },
}))

async function mountObservability(initialPath = '/system') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/system', component: ObservabilityView },
    ],
  })
  await router.push(initialPath)
  await router.isReady()
  const wrapper = mount(ObservabilityView, {
    global: {
      plugins: [router],
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('ObservabilityView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders runtime tab by default', async () => {
    const { wrapper } = await mountObservability()
    expect(wrapper.get('[data-testid="tab-runtime"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-runtime').exists()).toBe(true)
    expect(wrapper.text()).toContain('运行状态')
  })

  it('switches between tabs and updates router query', async () => {
    const { wrapper, router } = await mountObservability()

    // Switch to Statistics tab
    await wrapper.get('[data-testid="tab-statistics"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-statistics"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-statistics').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('statistics')

    // Switch to Live tab
    await wrapper.get('[data-testid="tab-live"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-live"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-live').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('live')

    // Switch to Audit tab
    await wrapper.get('[data-testid="tab-audit"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="tab-audit"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-audit').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('audit')
  })

  it('initializes with tab from route query', async () => {
    const { wrapper } = await mountObservability('/system?tab=statistics')
    expect(wrapper.get('[data-testid="tab-statistics"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.find('#tabpanel-statistics').exists()).toBe(true)
  })
})
