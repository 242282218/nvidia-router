import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { statisticsApi } from './api'
import StatisticsView from './StatisticsView.vue'
import type { MonitoringSnapshot, RequestLogsPage } from './types'

vi.mock('./api', () => ({
  statisticsApi: {
    getSummary: vi.fn(),
    getLogs: vi.fn(),
  },
}))

const snapshot: MonitoringSnapshot = {
  range: '24h',
  from: '2026-08-02T05:00:00Z',
  to: '2026-08-03T04:00:00Z',
  summary: {
    request_count: 1234,
    success_count: 1220,
    failure_count: 14,
    success_rate: 98.87,
    average_duration_ms: 921.4,
    average_first_byte_ms: 779.2,
    average_first_token_ms: 210.4,
    average_queue_ms: 13.5,
    total_attempts: 1260,
    prompt_tokens: 955700,
    completion_tokens: 3200,
    first_token_p50_ms: 120.5,
    first_token_p95_ms: 840.2,
  },
  series: [
    {
      bucket: '2026-08-03T03:00:00Z',
      request_count: 500,
      success_count: 498,
      failure_count: 2,
      average_duration_ms: 800,
      average_first_byte_ms: 700,
      average_first_token_ms: 180,
      average_queue_ms: 10,
      total_attempts: 510,
      prompt_tokens: 400000,
      completion_tokens: 1000,
    },
    {
      bucket: '2026-08-03T04:00:00Z',
      request_count: 734,
      success_count: 722,
      failure_count: 12,
      average_duration_ms: 1000,
      average_first_byte_ms: 800,
      average_first_token_ms: 230,
      average_queue_ms: 16,
      total_attempts: 750,
      prompt_tokens: 555700,
      completion_tokens: 2200,
    },
  ],
}

const logs: RequestLogsPage = {
  items: [{
    request_id: 'req-safe',
    endpoint: '/v1/chat/completions',
    model_id: 'meta/llama',
    access_key_id: 4,
    nvidia_key_id: 7,
    http_status: 200,
    outcome: 'success',
    is_stream: true,
    queue_ms: 13,
    first_byte_ms: 779,
    duration_ms: 921,
    attempt_count: 1,
    prompt_tokens: 100,
    completion_tokens: 20,
    upstream_request_id: 'up-safe',
    reasoning_requested: true,
    reasoning_wire_fields: 'thinking',
    reasoning_present: true,
    reasoning_chars: 95,
    stream_done: true,
    route_mode: 'direct',
    created_at: '2026-08-03T03:00:00.000Z',
  }],
  page: 1,
  page_size: 50,
  total: 1,
  has_more: false,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(statisticsApi.getSummary).mockResolvedValue({ data: snapshot })
  vi.mocked(statisticsApi.getLogs).mockResolvedValue({ data: logs })
})

describe('StatisticsView monitoring dashboard', () => {
  it('loads KPI cards, trends and request metadata', async () => {
    const wrapper = mount(StatisticsView)
    await flushPromises()

    expect(wrapper.text()).toContain('监控')
    expect(wrapper.text()).toContain('1,234')
    expect(wrapper.text()).toContain('98.9%')
    expect(wrapper.text()).toContain('TTFT P50')
    expect(wrapper.text()).toContain('120.5 ms')
    expect(wrapper.text()).toContain('TTFT P95')
    expect(wrapper.text()).toContain('840.2 ms')
    expect(wrapper.text()).toContain('请求趋势')
    expect(wrapper.text()).toContain('延迟趋势')
    expect(wrapper.get('[data-testid="monitoring-log-table"]').text()).toContain('req-safe')
    expect(wrapper.get('[data-testid="monitoring-log-table"]').text()).toContain('流式')
    expect(wrapper.get('[data-testid="monitoring-log-table"]').text()).toContain('up-safe')
    // Reasoning observability renders across the mobile card and desktop table.
    const logTable = wrapper.get('[data-testid="monitoring-log-table"]').text()
    expect(logTable).toContain('思考')
    expect(logTable).toContain('请求 是 · 响应 是 · 95 字')
    expect(logTable).toContain('thinking')
    expect(logTable).toContain('direct')
    expect(logTable).toContain('是 · [DONE]')
    expect(logTable).not.toContain('response body')
    expect(statisticsApi.getSummary).toHaveBeenCalledWith('24h', {}, expect.any(AbortSignal))
    expect(statisticsApi.getLogs).toHaveBeenCalledWith('24h', {}, 1, 50, expect.any(AbortSignal))
  })

  it('reloads both queries when the range changes and keeps filters', async () => {
    const wrapper = mount(StatisticsView)
    await flushPromises()
    await wrapper.get('[data-testid="monitoring-search"]').setValue('safe')
    await wrapper.get('[data-testid="monitoring-filters"]').trigger('submit')
    await flushPromises()

    await wrapper.get('[data-testid="range-30d"]').trigger('click')
    await flushPromises()

    expect(statisticsApi.getSummary).toHaveBeenLastCalledWith('30d', { search: 'safe' }, expect.any(AbortSignal))
    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('30d', { search: 'safe' }, 1, 50, expect.any(AbortSignal))
    expect((wrapper.get('[data-testid="monitoring-search"]').element as HTMLInputElement).value).toBe('safe')
  })

  it('submits status filters and paginates request logs', async () => {
    vi.mocked(statisticsApi.getLogs).mockResolvedValue({
      data: { ...logs, has_more: true, total: 51 },
    })
    const wrapper = mount(StatisticsView)
    await flushPromises()

    await wrapper.get('[data-testid="monitoring-status"]').setValue('failure')
    await wrapper.get('[data-testid="monitoring-filters"]').trigger('submit')
    await flushPromises()

    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('24h', { outcome: 'failure' }, 1, 50, expect.any(AbortSignal))
    await wrapper.get('[data-testid="monitoring-next-page"]').trigger('click')
    await flushPromises()
    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('24h', { outcome: 'failure' }, 2, 50, expect.any(AbortSignal))
  })

  it('rejects invalid numeric filters inline instead of silently dropping them', async () => {
    const wrapper = mount(StatisticsView)
    await flushPromises()
    const summaryCallsBefore = vi.mocked(statisticsApi.getSummary).mock.calls.length

    await wrapper.get('[data-testid="monitoring-status"]').setValue('failure')
    await wrapper.get('[data-testid="monitoring-status-code"]').setValue('-5')
    await wrapper.get('[data-testid="monitoring-filters"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="monitoring-filter-error"]').text()).toContain('状态码必须是正整数')
    // The invalid filter must not trigger a reload.
    expect(vi.mocked(statisticsApi.getSummary).mock.calls.length).toBe(summaryCallsBefore)
  })

  it('paginates via page numbers, page-size select, and a jump input', async () => {
    vi.mocked(statisticsApi.getLogs).mockResolvedValue({
      data: { ...logs, has_more: true, total: 220 },
    })
    const wrapper = mount(StatisticsView)
    await flushPromises()

    // Page-size change resets to page 1 and reloads with the new size.
    await wrapper.get('[data-testid="monitoring-page-size"]').setValue('100')
    await wrapper.get('[data-testid="monitoring-page-size"]').trigger('change')
    await flushPromises()
    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('24h', {}, 1, 100, expect.any(AbortSignal))

    // Direct page-number navigation.
    await wrapper.get('[data-testid="monitoring-page-2"]').trigger('click')
    await flushPromises()
    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('24h', {}, 2, 100, expect.any(AbortSignal))

    // Jump input navigates to a specific page.
    await wrapper.get('[data-testid="monitoring-jump-page"]').setValue('3')
    await wrapper.get('[data-testid="monitoring-jump-page"]').trigger('submit')
    await flushPromises()
    expect(statisticsApi.getLogs).toHaveBeenLastCalledWith('24h', {}, 3, 100, expect.any(AbortSignal))
  })

  it('shows summary and log errors independently', async () => {
    vi.mocked(statisticsApi.getSummary).mockRejectedValueOnce(new Error('summary failed'))
    vi.mocked(statisticsApi.getLogs).mockResolvedValueOnce({ data: { ...logs, items: [] } })
    const wrapper = mount(StatisticsView)
    await flushPromises()

    expect(wrapper.get('[data-testid="monitoring-summary-error"]').text()).toContain('监控汇总加载失败')
    expect(wrapper.get('[data-testid="monitoring-empty-logs"]').text()).toContain('暂无请求记录')
  })

  it('shows invalid payload errors instead of rendering unsafe data', async () => {
    vi.mocked(statisticsApi.getSummary).mockResolvedValueOnce({ data: { ...snapshot, series: null } } as never)
    const wrapper = mount(StatisticsView)
    await flushPromises()

    expect(wrapper.get('[data-testid="monitoring-summary-error"]').text()).toContain('监控汇总加载失败')
    expect(wrapper.text()).not.toContain('response body')
  })

  it('keeps the last good data during a reload and flags stale background polls', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = mount(StatisticsView)
      await flushPromises()
      expect(wrapper.text()).toContain('1,234')

      // A background poll failure must keep the numbers on screen but flag them.
      vi.mocked(statisticsApi.getSummary).mockRejectedValueOnce(new Error('poll failed'))
      await vi.advanceTimersByTimeAsync(30_000)
      const stale = wrapper.get('[data-testid="monitoring-summary-stale"]')
      expect(stale.text()).toContain('未更新')
      expect(wrapper.text()).toContain('1,234')

      // The retry recovers and clears the stale banner.
      vi.mocked(statisticsApi.getSummary).mockResolvedValue({ data: snapshot })
      await wrapper.get('[data-testid="monitoring-summary-stale"] button').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-testid="monitoring-summary-stale"]').exists()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })
})
