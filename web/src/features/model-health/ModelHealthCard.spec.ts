import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ModelHealthCard from './ModelHealthCard.vue'
import type { ModelHealthModel } from './types'

const model: ModelHealthModel = {
  model_id: 7,
  public_id: 'nvidia/model-7',
  display_name: 'Model 7',
  kind: 'chat',
  provider: 'nvidia',
  enabled: true,
  status: 'degraded',
  success_rate: 75,
  probe_count: 4,
  success_count: 3,
  failure_count: 1,
  timeout_count: 0,
  skipped_count: 0,
  last_probe_at: '2026-08-20T04:00:00Z',
  last_duration_ms: 120,
  last_error_code: 'probe_failed',
  consecutive_failures: 0,
  buckets: [
    { start: '2026-08-20T03:00:00Z', end: '2026-08-20T03:06:00Z', outcome: 'success', probe_count: 1, success_count: 1, failure_count: 0, timeout_count: 0, average_duration_ms: 120 },
    { start: '2026-08-20T03:06:00Z', end: '2026-08-20T03:12:00Z', outcome: 'mixed', probe_count: 2, success_count: 1, failure_count: 1, timeout_count: 0, average_duration_ms: 180 },
    { start: '2026-08-20T03:12:00Z', end: '2026-08-20T03:18:00Z', outcome: 'empty', probe_count: 0, success_count: 0, failure_count: 0, timeout_count: 0, average_duration_ms: 0 },
  ],
}

describe('ModelHealthCard', () => {
  it('shows status text, probe metrics, and an accessible timeline description', () => {
    const wrapper = mount(ModelHealthCard, { props: { model } })

    expect(wrapper.text()).toContain('降级')
    expect(wrapper.text()).toContain('75.0%')
    expect(wrapper.text()).toContain('探测次数')
    expect(wrapper.text()).toContain('连续失败')
    expect(wrapper.text()).toContain('最近异常：探测失败')
    expect(wrapper.get('[data-testid="model-health-timeline-7"]').attributes('aria-label')).toContain('3 个时间段')
    expect(wrapper.get('[data-testid="model-health-bucket-7-1"]').attributes('title')).toContain('部分成功')
  })

  it('keeps the card compact without an expandable timeline detail list', () => {
    const wrapper = mount(ModelHealthCard, { props: { model } })

    expect(wrapper.text()).not.toContain('时间段详情')
    expect(wrapper.text()).not.toContain('收起')
    expect(wrapper.find('[aria-expanded]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="model-health-bucket-7-1"]').element.tagName).toBe('SPAN')
  })

  it('fits all sixty timeline buckets inside the compact track', () => {
    const templateBucket = model.buckets[0]!
    const buckets = Array.from({ length: 60 }, (_, index) => ({
      ...templateBucket,
      start: `2026-08-20T${String(3 + Math.floor(index / 6)).padStart(2, '0')}:${String((index % 6) * 10).padStart(2, '0')}:00Z`,
      end: `2026-08-20T${String(3 + Math.floor(index / 6)).padStart(2, '0')}:${String((index % 6) * 10 + 5).padStart(2, '0')}:00Z`,
    }))
    const wrapper = mount(ModelHealthCard, { props: { model: { ...model, buckets } } })
    const timeline = wrapper.get('[data-testid="model-health-timeline-7"]')

    expect(timeline.findAll('[role="img"]')).toHaveLength(60)
    expect(timeline.get('[role="img"]').classes()).toContain('min-w-0')
  })

  it('labels an unconfigured model without relying on color alone', () => {
    const wrapper = mount(ModelHealthCard, { props: { model: { ...model, status: 'unconfigured', probe_count: 1, success_rate: 0, last_error_code: 'no_credential' } } })

    // WIP 统一文案：unchecked/stale/unconfigured 一律显示「无数据」，
    // 不再依赖颜色或具体原因文案区分。
    expect(wrapper.text()).toContain('无数据')
    expect(wrapper.text()).toContain('0.0%')
  })
})
