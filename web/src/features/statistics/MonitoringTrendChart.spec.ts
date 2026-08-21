import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import MonitoringTrendChart from './MonitoringTrendChart.vue'
import type { MonitoringSeriesPoint } from './types'

const point = (bucket: string, requestCount: number): MonitoringSeriesPoint => ({
  bucket,
  request_count: requestCount,
  success_count: requestCount,
  failure_count: 0,
  canceled_count: 0,
  average_duration_ms: 120,
  average_first_byte_ms: 80,
  average_first_token_ms: 60,
  average_queue_ms: 10,
  total_attempts: requestCount,
  prompt_tokens: 100,
  completion_tokens: 20,
})

describe('MonitoringTrendChart', () => {
  it('renders a smooth area line and an accessible data table', () => {
    const wrapper = mount(MonitoringTrendChart, {
      props: {
        series: [point('2026-08-03T10:00:00Z', 2), point('2026-08-03T11:00:00Z', 8)],
        metric: 'requests',
        title: '请求趋势',
      },
    })

    expect(wrapper.find('svg').exists()).toBe(true)
    // 平滑曲线 + 渐变面积都以 path 渲染；曲线起点从左侧留白开始
    const paths = wrapper.findAll('path')
    expect(paths.length).toBeGreaterThanOrEqual(2)
    const strokePath = paths.find((candidate) => candidate.attributes('stroke') !== undefined)
    expect(strokePath?.attributes('d')).toMatch(/^M48/)
    expect(wrapper.find('details table').text()).toContain('2026-08-03T11:00:00Z')
    expect(wrapper.text()).toContain('请求趋势')
  })

  it('shows a clear empty state without drawing a misleading line', () => {
    const wrapper = mount(MonitoringTrendChart, {
      props: { series: [], metric: 'latency', title: '延迟趋势' },
    })

    expect(wrapper.text()).toContain('暂无趋势数据')
    expect(wrapper.find('svg').exists()).toBe(false)
  })
})
