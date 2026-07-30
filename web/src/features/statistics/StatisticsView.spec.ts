import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { statisticsApi } from './api'
import StatisticsView from './StatisticsView.vue'

vi.mock('./api', () => ({
  statisticsApi: {
    getDaily: vi.fn(),
    getRecentErrors: vi.fn(),
  },
}))

const base = {
  day: '2026-07-30',
  request_count: 10,
  success_count: 8,
  failure_count: 2,
  average_duration_ms: 120.5,
  average_queue_ms: 20.5,
  average_attempts: 1.25,
  prompt_tokens: 100,
  completion_tokens: 50,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(statisticsApi.getDaily).mockResolvedValue({
    data: [
      { ...base, dimension_type: 'global', dimension_id: 'all' },
      { ...base, dimension_type: 'model', dimension_id: 'meta/llama' },
      { ...base, dimension_type: 'nvidia_key', dimension_id: '7' },
      {
        ...base,
        dimension_type: 'access_key',
        dimension_id: '4',
        prompt_tokens: 0,
        completion_tokens: 0,
      },
    ],
  })
  vi.mocked(statisticsApi.getRecentErrors).mockResolvedValue({
    data: [
      {
        request_id: 'req-safe',
        endpoint: '/v1/chat/completions',
        model_id: 'meta/llama',
        nvidia_key_id: 7,
        access_key_id: 4,
        http_status: 429,
        error_code: 'rate_limited',
        upstream_request_id: 'up-safe',
        created_at: '2026-07-30T09:00:00Z',
      },
    ],
  })
})

describe('StatisticsView', () => {
  it('shows all four dimensions and only the planned aggregate metrics', async () => {
    const wrapper = mount(StatisticsView)
    await flushPromises()

    for (const title of ['总体', '模型', 'NVIDIA Key', 'Access Key']) {
      expect(wrapper.text()).toContain(title)
    }
    const global = wrapper.get('[data-testid="statistics-global"]')
    expect(global.text()).toContain('10')
    expect(global.text()).toContain('8')
    expect(global.text()).toContain('2')
    expect(global.text()).toContain('80.0%')
    expect(global.text()).toContain('120.5 ms')
    expect(global.text()).toContain('20.5 ms')
    expect(global.text()).toContain('1.25')
    expect(global.text()).toContain('100 / 50')
    expect(wrapper.get('[data-testid="statistics-access_key"]').text()).toContain('—')
  })

  it('shows recent safe error metadata without request or response bodies', async () => {
    const wrapper = mount(StatisticsView)
    await flushPromises()

    const errors = wrapper.get('[data-testid="recent-errors"]')
    expect(errors.text()).toContain('req-safe')
    expect(errors.text()).toContain('/v1/chat/completions')
    expect(errors.text()).toContain('rate_limited')
    expect(errors.text()).toContain('429')
    expect(errors.text()).not.toContain('prompt')
    expect(errors.text()).not.toContain('response body')
    expect(statisticsApi.getDaily).toHaveBeenCalledWith(30)
    expect(statisticsApi.getRecentErrors).toHaveBeenCalledWith(50)
  })
})
