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
    expect(wrapper.text()).toContain('4 次探测')
    expect(wrapper.text()).toContain('最近异常：探测失败')
    expect(wrapper.get('[data-testid="model-health-timeline-7"]').attributes('aria-label')).toContain('3 个时间段')
    expect(wrapper.get('[data-testid="model-health-bucket-7-1"]').attributes('title')).toContain('部分成功')
    expect(wrapper.get('[data-testid="model-health-timeline-details-7"]').text()).toContain('部分成功')
  })

  it('labels an unconfigured model without relying on color alone', () => {
    const wrapper = mount(ModelHealthCard, { props: { model: { ...model, status: 'unconfigured', probe_count: 1, success_rate: 0, last_error_code: 'no_credential' } } })

    expect(wrapper.text()).toContain('未配置')
    expect(wrapper.text()).toContain('缺少可用凭据')
  })
})
