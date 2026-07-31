import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { statisticsApi } from './api'
import StatisticsView from './StatisticsView.vue'
import type { DailyStatistic, RecentError } from './types'

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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
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
  it.each([
    ['a non-array data field', { data: null }],
    ['a null numeric aggregate', {
      data: [{ ...base, dimension_type: 'global', dimension_id: 'all', average_duration_ms: null }],
    }],
  ])('shows a visible statistics error for %s in a successful response', async (_name, response) => {
    vi.mocked(statisticsApi.getDaily).mockResolvedValue(response as never)
    const wrapper = mount(StatisticsView)
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('统计数据加载失败')
    expect(wrapper.get('[data-testid="recent-errors"]').text()).toContain('req-safe')
  })

  it('shows a visible recent-errors error for a null numeric status in a successful response', async () => {
    vi.mocked(statisticsApi.getRecentErrors).mockResolvedValue({
      data: [{
        request_id: 'req-invalid',
        endpoint: '/v1/chat/completions',
        http_status: null,
        error_code: 'rate_limited',
        created_at: '2026-07-30T09:00:00Z',
      }],
    } as never)
    const wrapper = mount(StatisticsView)
    await flushPromises()

    expect(wrapper.get('[data-testid="recent-errors"]').text()).toContain('最近错误加载失败')
    expect(wrapper.text()).not.toContain('req-invalid')
  })

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

  it('includes the statistic dimension in each row key', async () => {
    vi.mocked(statisticsApi.getDaily).mockResolvedValue({
      data: [
        { ...base, dimension_type: 'global', dimension_id: 'shared-id' },
        { ...base, dimension_type: 'model', dimension_id: 'shared-id' },
      ],
    })
    const wrapper = mount(StatisticsView)
    await flushPromises()

    const rows = [
      ...wrapper.get('[data-testid="statistics-global"]').findAll('tbody tr'),
      ...wrapper.get('[data-testid="statistics-model"]').findAll('tbody tr'),
    ]
    const keys = rows.map((row) => (row.element as HTMLElement & {
      __vnode?: { key?: unknown }
    }).__vnode?.key)
    expect(keys).toEqual([
      '2026-07-30-global-shared-id',
      '2026-07-30-model-shared-id',
    ])
  })

  it('does not update statistics state after unmount when requests resolve', async () => {
    const daily = deferred<{ data: DailyStatistic[] }>()
    const errors = deferred<{ data: RecentError[] }>()
    vi.mocked(statisticsApi.getDaily).mockReset().mockReturnValueOnce(daily.promise as never)
    vi.mocked(statisticsApi.getRecentErrors).mockReset().mockReturnValueOnce(errors.promise as never)
    const wrapper = mount(StatisticsView)
    const state = wrapper.vm as unknown as {
      statistics: DailyStatistic[]
      recentErrors: RecentError[]
      statisticsError: string
      errorsError: string
      loading: boolean
    }

    wrapper.unmount()
    daily.resolve({ data: [{ ...base, dimension_type: 'global', dimension_id: 'all' }] })
    errors.resolve({ data: [] })
    await flushPromises()

    expect(state.statistics).toEqual([])
    expect(state.recentErrors).toEqual([])
    expect(state.statisticsError).toBe('')
    expect(state.errorsError).toBe('')
    expect(state.loading).toBe(true)
  })

  it('does not update error or loading state after unmount when requests reject', async () => {
    const daily = deferred<never>()
    const errors = deferred<never>()
    vi.mocked(statisticsApi.getDaily).mockReset().mockReturnValueOnce(daily.promise as never)
    vi.mocked(statisticsApi.getRecentErrors).mockReset().mockReturnValueOnce(errors.promise as never)
    const wrapper = mount(StatisticsView)
    const state = wrapper.vm as unknown as {
      statistics: DailyStatistic[]
      recentErrors: RecentError[]
      statisticsError: string
      errorsError: string
      loading: boolean
    }

    wrapper.unmount()
    daily.reject(new Error('daily failed'))
    errors.reject(new Error('errors failed'))
    await flushPromises()

    expect(state.statistics).toEqual([])
    expect(state.recentErrors).toEqual([])
    expect(state.statisticsError).toBe('')
    expect(state.errorsError).toBe('')
    expect(state.loading).toBe(true)
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
