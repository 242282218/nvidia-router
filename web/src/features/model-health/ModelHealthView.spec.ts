import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ModelHealthView from './ModelHealthView.vue'
import { modelHealthApi } from './api'
import type { ModelHealthSummary } from './types'

vi.mock('./api', () => ({
  modelHealthApi: {
    getSummary: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    runNow: vi.fn(),
  },
}))

vi.mock('../../shared/usePolling', () => ({
  usePolling: vi.fn(),
}))

const summary: ModelHealthSummary = {
  range: '6h',
  from: '2026-08-20T00:00:00Z',
  to: '2026-08-20T06:00:00Z',
  total_models: 2,
  healthy_count: 1,
  degraded_count: 1,
  unavailable_count: 0,
  unchecked_count: 0,
  stale_count: 0,
  unconfigured_count: 0,
  settings: { enabled: false, interval_seconds: 60, concurrency: 2, updated_at: '2026-08-20T06:00:00Z' },
  models: [
    {
      model_id: 1,
      public_id: 'model/healthy',
      display_name: 'Healthy Model',
      kind: 'chat',
      provider: 'nvidia',
      enabled: true,
      status: 'healthy',
      success_rate: 100,
      probe_count: 4,
      success_count: 4,
      failure_count: 0,
      timeout_count: 0,
      skipped_count: 0,
      last_probe_at: '2026-08-20T05:59:00Z',
      last_duration_ms: 120,
      consecutive_failures: 0,
      buckets: [],
    },
    {
      model_id: 2,
      public_id: 'model/degraded',
      display_name: 'Degraded Model',
      kind: 'chat',
      provider: 'nvidia',
      enabled: false,
      status: 'degraded',
      success_rate: 80,
      probe_count: 5,
      success_count: 4,
      failure_count: 1,
      timeout_count: 0,
      skipped_count: 0,
      last_probe_at: '2026-08-20T05:58:00Z',
      last_duration_ms: 500,
      consecutive_failures: 0,
      buckets: [],
    },
  ],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(modelHealthApi.getSummary).mockResolvedValue({ data: summary })
  vi.mocked(modelHealthApi.getSettings).mockResolvedValue({ data: summary.settings })
  vi.mocked(modelHealthApi.updateSettings).mockImplementation(async (next) => ({ data: { ...summary.settings, ...next } }))
  vi.mocked(modelHealthApi.runNow).mockResolvedValue({ data: { accepted: true } })
})

describe('ModelHealthView', () => {
  it('loads all whitelist models and renders the health summary', async () => {
    const wrapper = mount(ModelHealthView)
    await flushPromises()

    expect(wrapper.text()).toContain('渠道状态')
    expect(wrapper.text()).toContain('监控 2 个模型')
    expect(wrapper.get('[data-testid="model-health-card-1"]').text()).toContain('Healthy Model')
    expect(wrapper.get('[data-testid="model-health-card-2"]').text()).toContain('停用')
    expect(modelHealthApi.getSummary).toHaveBeenCalledWith('6h', 'default', 'availability', expect.any(AbortSignal))
  })

  it('reloads when range, grouping, or sorting changes', async () => {
    const wrapper = mount(ModelHealthView)
    await flushPromises()

    await wrapper.get('[data-testid="model-health-range"]').setValue('24h')
    await wrapper.get('[data-testid="model-health-group"]').setValue('provider')
    await wrapper.get('[data-testid="model-health-sort"]').setValue('recent')
    await flushPromises()

    expect(modelHealthApi.getSummary).toHaveBeenLastCalledWith('24h', 'provider', 'recent', expect.any(AbortSignal))
  })

  it('persists detection switch and frequency, and can trigger an immediate run', async () => {
    const wrapper = mount(ModelHealthView)
    await flushPromises()

    await wrapper.get('[data-testid="model-health-enabled"]').trigger('click')
    await flushPromises()
    expect(modelHealthApi.updateSettings).toHaveBeenCalledWith({ enabled: true, interval_seconds: 60, concurrency: 2 }, expect.any(AbortSignal))

    await wrapper.get('[data-testid="model-health-interval"]').setValue('300')
    await flushPromises()
    expect(modelHealthApi.updateSettings).toHaveBeenLastCalledWith({ enabled: true, interval_seconds: 300, concurrency: 2 }, expect.any(AbortSignal))

    await wrapper.get('[data-testid="model-health-run"]').trigger('click')
    await flushPromises()
    expect(modelHealthApi.runNow).toHaveBeenCalledOnce()
  })

  it('keeps and edits a legal custom frequency that is not a preset', async () => {
    const customSummary = {
      ...summary,
      settings: { ...summary.settings, enabled: false, interval_seconds: 45 },
    }
    vi.mocked(modelHealthApi.getSummary).mockResolvedValueOnce({ data: customSummary })
    const wrapper = mount(ModelHealthView)
    await flushPromises()

    const interval = wrapper.get('[data-testid="model-health-interval"]')
    expect(interval.element.tagName).toBe('INPUT')
    expect((interval.element as HTMLInputElement).value).toBe('45')

    await interval.setValue('75')
    await flushPromises()
    expect(modelHealthApi.updateSettings).toHaveBeenLastCalledWith({ enabled: false, interval_seconds: 75, concurrency: 2 }, expect.any(AbortSignal))
  })

  it('shows a recoverable error when the summary is invalid', async () => {
    vi.mocked(modelHealthApi.getSummary).mockRejectedValueOnce(new Error('summary failed'))
    const wrapper = mount(ModelHealthView)
    await flushPromises()

    expect(wrapper.get('[data-testid="model-health-error"]').text()).toContain('渠道状态加载失败')
    expect(wrapper.find('[data-testid="model-health-retry"]').exists()).toBe(true)
  })
})
