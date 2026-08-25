import { mount, type VueWrapper } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import SettingsForm from './SettingsForm.vue'
import type { RuntimeSettings } from './types'

const settings: RuntimeSettings = {
  queue_capacity: 100,
  queue_wait_timeout_ms: 60_000,
  connect_timeout_ms: 10_000,
  first_byte_timeout_ms: 60_000,
  nonstream_total_timeout_ms: 300_000,
  shutdown_grace_ms: 60_000,
  failover_status_codes: '429,500,502,503,504',
  request_log_retention_days: 30,
  max_attempts_per_request: 5,
  retry_budget_ms: 120_000,
  max_streaming_per_key: 2,
  stream_first_token_timeout_ms: 60_000,
  stream_idle_timeout_ms: 180_000,
  latency_routing_enabled: true,
  embedding_cache_enabled: false,
  embedding_cache_max_entries: 256,
  auto_reasoning_enabled: false,
  capability_probe_enabled: false,
}

const inputTestIds = {
  queue_capacity: 'queue-capacity',
  queue_wait_timeout_seconds: 'queue-wait-seconds',
  connect_timeout_seconds: 'connect-timeout-seconds',
  first_byte_timeout_seconds: 'first-byte-timeout-seconds',
  nonstream_total_timeout_minutes: 'nonstream-timeout-minutes',
  shutdown_grace_seconds: 'shutdown-grace-seconds',
  request_log_retention_days: 'request-log-retention-days',
  max_attempts_per_request: 'max-attempts-per-request',
  retry_budget_ms: 'retry-budget-ms',
  max_streaming_per_key: 'max-streaming-per-key',
  stream_first_token_timeout_seconds: 'stream-first-token-timeout-seconds',
  stream_idle_timeout_seconds: 'stream-idle-timeout-seconds',
} as const

function mountForm() {
  return mount(SettingsForm, {
    props: {
      settings,
      saving: false,
      fieldErrors: {},
      formError: '',
    },
  })
}

async function setFields(
  wrapper: VueWrapper,
  values: Partial<Record<keyof typeof inputTestIds, number>>,
): Promise<void> {
  for (const [field, value] of Object.entries(values)) {
    await wrapper.get(`[data-testid="${inputTestIds[field as keyof typeof inputTestIds]}"]`)
      .setValue(String(value))
  }
}

describe('SettingsForm', () => {
  it('rejects an empty numeric input instead of converting it to zero', async () => {
    const wrapper = mountForm()

    await wrapper.get('[data-testid="queue-capacity"]').setValue('')
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get('[data-testid="error-queue_capacity"]').text()).toContain('请输入')
  })

  it('saves failover codes and request-log retention days', async () => {
    const wrapper = mountForm()

    await wrapper.get('[data-testid="failover-status-codes"]').setValue('429,403,500-599')
    await wrapper.get('[data-testid="request-log-retention-days"]').setValue('60')
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toEqual([[
      expect.objectContaining({
        failover_status_codes: '429,403,500-599',
        request_log_retention_days: 60,
      }),
    ]])
  })

  it.each([Number.NaN, Number.POSITIVE_INFINITY])(
    'rejects the non-finite queue capacity %s',
    async (value) => {
      const wrapper = mountForm()
      const state = wrapper.vm as unknown as {
        fields: { queue_capacity: number }
      }
      state.fields.queue_capacity = value

      await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

      expect(wrapper.emitted('save')).toBeUndefined()
      expect(wrapper.get('[data-testid="error-queue_capacity"]').text()).toContain('请输入')
    },
  )

  it.each([
    ['queue-capacity', 'error-queue_capacity'],
    ['queue-wait-seconds', 'error-queue_wait_timeout_ms'],
    ['connect-timeout-seconds', 'error-connect_timeout_ms'],
    ['first-byte-timeout-seconds', 'error-first_byte_timeout_ms'],
    ['shutdown-grace-seconds', 'error-shutdown_grace_ms'],
  ])('rejects a fractional value for integer input %s', async (inputId, errorId) => {
    const wrapper = mountForm()

    await wrapper.get(`[data-testid="${inputId}"]`).setValue('1.5')
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get(`[data-testid="${errorId}"]`).text()).toContain('整数')
  })

  it.each([
    [
      {
        queue_capacity: 1,
        queue_wait_timeout_seconds: 1,
        connect_timeout_seconds: 1,
        first_byte_timeout_seconds: 1,
        nonstream_total_timeout_minutes: 1 / 60,
        shutdown_grace_seconds: 1,
        max_attempts_per_request: 1,
        retry_budget_ms: 1_000,
        max_streaming_per_key: 2,
        stream_first_token_timeout_seconds: 1,
        stream_idle_timeout_seconds: 1,
      },
      {
        queue_capacity: 1,
        queue_wait_timeout_ms: 1_000,
        connect_timeout_ms: 1_000,
        first_byte_timeout_ms: 1_000,
        nonstream_total_timeout_ms: 1_000,
        shutdown_grace_ms: 1_000,
        failover_status_codes: '429,500,502,503,504',
        request_log_retention_days: 30,
        max_attempts_per_request: 1,
        retry_budget_ms: 1_000,
        max_streaming_per_key: 2,
        stream_first_token_timeout_ms: 1_000,
        stream_idle_timeout_ms: 1_000,
        latency_routing_enabled: true,
        embedding_cache_enabled: false,
        embedding_cache_max_entries: 256,
        auto_reasoning_enabled: false,
      },
    ],
    [
      {
        queue_capacity: 10_000,
        queue_wait_timeout_seconds: 600,
        connect_timeout_seconds: 120,
        first_byte_timeout_seconds: 600,
        nonstream_total_timeout_minutes: 30,
        shutdown_grace_seconds: 600,
        max_attempts_per_request: 50,
        retry_budget_ms: 600_000,
        max_streaming_per_key: 10,
        stream_first_token_timeout_seconds: 1_800,
        stream_idle_timeout_seconds: 1_800,
      },
      {
        queue_capacity: 10_000,
        queue_wait_timeout_ms: 600_000,
        connect_timeout_ms: 120_000,
        first_byte_timeout_ms: 600_000,
        nonstream_total_timeout_ms: 1_800_000,
        shutdown_grace_ms: 600_000,
        failover_status_codes: '429,500,502,503,504',
        request_log_retention_days: 30,
        max_attempts_per_request: 50,
        retry_budget_ms: 600_000,
        max_streaming_per_key: 10,
        stream_first_token_timeout_ms: 1_800_000,
        stream_idle_timeout_ms: 1_800_000,
        latency_routing_enabled: true,
        embedding_cache_enabled: false,
        embedding_cache_max_entries: 256,
        auto_reasoning_enabled: false,
      },
    ],
  ])('accepts exact settings boundaries', async (fields, expected) => {
    const wrapper = mountForm()
    await setFields(wrapper, fields)

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toEqual([[expected]])
  })

  it.each([
    ['queue-capacity', '0', 'error-queue_capacity'],
    ['nonstream-timeout-minutes', '30.1', 'error-nonstream_total_timeout_ms'],
  ])('rejects out-of-range value %s=%s', async (inputId, value, errorId) => {
    const wrapper = mountForm()

    await wrapper.get(`[data-testid="${inputId}"]`).setValue(value)
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get(`[data-testid="${errorId}"]`).text()).toContain('范围')
  })

  it.each(['29', '1'])('rejects request-log retention below 30 days: %s', async (value) => {
    const wrapper = mountForm()

    await wrapper.get('[data-testid="request-log-retention-days"]').setValue(value)
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get('[data-testid="error-request_log_retention_days"]').text()).toContain('范围')
  })

  it('renders attempt cap and retry budget with their backend values', () => {
    const wrapper = mountForm()

    expect((wrapper.get('[data-testid="max-attempts-per-request"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('[data-testid="retry-budget-ms"]').element as HTMLInputElement).value).toBe('120000')
    expect(wrapper.get('[data-testid="hint-max_attempts_per_request"]').text()).toContain('1-50')
    expect(wrapper.get('[data-testid="hint-retry_budget_ms"]').text()).toContain('1000-600000')
  })

  it('explains that the routing switch uses request quality for proxy exits', () => {
    const wrapper = mountForm()

    expect(wrapper.text()).toContain('质量感知调度')
    expect(wrapper.text()).toContain('真实请求质量')
  })

  it('saves attempt cap and retry budget', async () => {
    const wrapper = mountForm()

    await wrapper.get('[data-testid="max-attempts-per-request"]').setValue('3')
    await wrapper.get('[data-testid="retry-budget-ms"]').setValue('45000')
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toEqual([[
      expect.objectContaining({
        max_attempts_per_request: 3,
        retry_budget_ms: 45_000,
      }),
    ]])
  })

  it.each([
    ['max-attempts-per-request', '0', 'error-max_attempts_per_request'],
    ['max-attempts-per-request', '51', 'error-max_attempts_per_request'],
    ['retry-budget-ms', '999', 'error-retry_budget_ms'],
    ['retry-budget-ms', '600001', 'error-retry_budget_ms'],
  ])('rejects out-of-range retry setting %s=%s', async (inputId, value, errorId) => {
    const wrapper = mountForm()

    await wrapper.get(`[data-testid="${inputId}"]`).setValue(value)
    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.get(`[data-testid="${errorId}"]`).text()).toContain('范围')
  })
})
