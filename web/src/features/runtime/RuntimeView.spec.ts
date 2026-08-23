import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/client'
import { modelHealthApi } from '../model-health/api'
import type { ModelHealthSummaryResponse } from '../model-health/types'
import { runtimeApi } from './api'
import RuntimeView from './RuntimeView.vue'

// The channel-health card renders a router-link to /channel-status; stub it
// so view specs do not need a full router instance.
function mountView() {
  return mount(RuntimeView, { global: { stubs: { RouterLink: true } } })
}

vi.mock('./api', () => ({
  runtimeApi: {
    getSummary: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
  },
}))

vi.mock('../model-health/api', () => ({
  modelHealthApi: {
    getSummary: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    runNow: vi.fn(),
  },
}))

const settings = {
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
}

// 渠道健康摘要：2 个健康 + 1 个不可用（success_rate 40%），KPI 应显示 2 / 3
// 且问题列表出现该模型。
const channelHealthSummary: ModelHealthSummaryResponse = {
  data: {
    range: '24h',
    from: '2026-07-29T10:00:00Z',
    to: '2026-07-30T10:00:00Z',
    total_models: 3,
    healthy_count: 2,
    degraded_count: 0,
    unavailable_count: 1,
    unchecked_count: 0,
    stale_count: 0,
    unconfigured_count: 0,
    models: [
      {
        model_id: 1,
        public_id: 'glm-5.2',
        display_name: 'GLM 5.2',
        kind: 'chat',
        provider: 'nvidia',
        enabled: true,
        status: 'healthy',
        success_rate: 99.5,
        probe_count: 20,
        success_count: 19,
        failure_count: 1,
        timeout_count: 0,
        skipped_count: 0,
        last_probe_at: '2026-07-30T09:58:00Z',
        last_duration_ms: 1200,
        consecutive_failures: 0,
        buckets: [],
      },
      {
        model_id: 2,
        public_id: 'minimax-m3',
        display_name: 'MiniMax M3',
        kind: 'chat',
        provider: 'nvidia',
        enabled: true,
        status: 'healthy',
        success_rate: 96,
        probe_count: 20,
        success_count: 19,
        failure_count: 1,
        timeout_count: 0,
        skipped_count: 0,
        last_probe_at: '2026-07-30T09:58:00Z',
        last_duration_ms: 900,
        consecutive_failures: 0,
        buckets: [],
      },
      {
        model_id: 3,
        public_id: 'kimi-k2',
        display_name: 'Kimi K2',
        kind: 'chat',
        provider: 'nvidia',
        enabled: true,
        status: 'degraded',
        success_rate: 40,
        probe_count: 20,
        success_count: 8,
        failure_count: 12,
        timeout_count: 0,
        skipped_count: 0,
        last_probe_at: '2026-07-30T09:58:00Z',
        last_duration_ms: 4000,
        consecutive_failures: 5,
        buckets: [],
      },
    ],
    settings: { enabled: true, interval_seconds: 300, concurrency: 2 },
  },
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(runtimeApi.getSummary).mockResolvedValue({
    data: {
      keys: { total: 8, enabled: 7, disabled: 1, auth_invalid: 2, cooling_down: 3, ready: 2 },
      active: 4,
      queue: { length: 5, capacity: 100 },
      earliest_cooldown: '2026-07-30T10:00:00Z',
      shutting_down: true,
    },
  })
  vi.mocked(runtimeApi.getSettings).mockResolvedValue({ data: settings })
  vi.mocked(runtimeApi.updateSettings).mockResolvedValue({ data: settings })
  vi.mocked(modelHealthApi.getSummary).mockResolvedValue(channelHealthSummary)
})

describe('RuntimeView', () => {
  it('shows key, active, queue, cooldown, channel health and shutdown state', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="runtime-key-counts"]').text()).toContain('2 / 8')
    expect(wrapper.get('[data-testid="runtime-key-counts"]').text()).toContain('启用 7')
    expect(wrapper.get('[data-testid="runtime-active"]').text()).toContain('4')
    expect(wrapper.get('[data-testid="runtime-queue"]').text()).toContain('5 / 100')
    expect(wrapper.get('[data-testid="runtime-cooldown"]').text()).toContain('3')
    expect(wrapper.get('[data-testid="runtime-cooldown"]').text()).toContain('2026/07/30')
    expect(wrapper.get('[data-testid="runtime-channel-health"]').text()).toContain('2 / 3')
    expect(wrapper.get('[data-testid="runtime-shutdown"]').text()).toContain('关闭中')
    // Key 池分布行 + 渠道问题列表
    expect(wrapper.findAll('[data-testid="runtime-key-pool-row"]')).toHaveLength(4)
    expect(wrapper.get('[data-testid="runtime-channel-problems"]').text()).toContain('Kimi K2')
  })

  it('lets the channel-health header copy shrink beside its action on narrow screens', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[aria-label="渠道健康"] > div > div').classes()).toContain('min-w-0')
  })

  it('lets the channel-health card shrink as a grid item on narrow screens', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[aria-label="渠道健康"]').classes()).toContain('min-w-0')
  })

  it('shows settings when summary loading fails', async () => {
    vi.mocked(runtimeApi.getSummary).mockRejectedValue(new ApiError(503, {
      type: 'server_error',
      code: 'summary_unavailable',
      message: '运行摘要暂时不可用。',
      param: null,
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('运行摘要暂时不可用')
    expect(wrapper.find('[data-testid="runtime-active"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="runtime-settings-form"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="queue-capacity"]').element).toBeTruthy()
  })

  it('shows summary when settings loading fails', async () => {
    vi.mocked(runtimeApi.getSettings).mockRejectedValue(new ApiError(503, {
      type: 'server_error',
      code: 'settings_unavailable',
      message: '运行设置暂时不可用。',
      param: null,
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('运行设置暂时不可用')
    expect(wrapper.get('[data-testid="runtime-active"]').text()).toContain('4')
    expect(wrapper.find('[data-testid="runtime-settings-form"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="runtime-settings-form"] button').attributes('disabled')).toBeDefined()
  })

  it('keeps rendering when channel health fails (supplementary telemetry)', async () => {
    vi.mocked(modelHealthApi.getSummary).mockRejectedValue(new ApiError(503, {
      type: 'server_error',
      code: 'model_health_unavailable',
      message: '渠道健康暂时不可用。',
      param: null,
    }))
    const wrapper = mountView()
    await flushPromises()

    // 渠道健康是补充遥测：失败只降级为该卡的空态，不拖垮整页。
    expect(wrapper.get('[data-testid="runtime-channel-health"]').text()).toContain('—')
    expect(wrapper.get('[data-testid="runtime-active"]').text()).toContain('4')
  })

  it('edits all settings in UI units and converts them to backend milliseconds', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect((wrapper.get('[data-testid="queue-capacity"]').element as HTMLInputElement).value).toBe('100')
    expect((wrapper.get('[data-testid="queue-wait-seconds"]').element as HTMLInputElement).value).toBe('60')
    expect((wrapper.get('[data-testid="connect-timeout-seconds"]').element as HTMLInputElement).value).toBe('10')
    expect((wrapper.get('[data-testid="first-byte-timeout-seconds"]').element as HTMLInputElement).value).toBe('60')
    expect((wrapper.get('[data-testid="nonstream-timeout-minutes"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('[data-testid="shutdown-grace-seconds"]').element as HTMLInputElement).value).toBe('60')
    expect((wrapper.get('[data-testid="request-log-retention-days"]').element as HTMLInputElement).value).toBe('30')

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')
    await flushPromises()

    // The form intentionally does not manage max_streaming_per_key yet (the
    // streaming-quota UI is a separate front-end task); the emitted payload
    // omits it and the server keeps its stored value on PATCH.
    expect(runtimeApi.updateSettings).toHaveBeenCalledWith(
      expect.objectContaining({
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
      }),
      expect.any(AbortSignal),
    )
  })

  it('keeps save success even when the summary refresh fails (audit #63)', async () => {
    vi.mocked(runtimeApi.getSummary)
      .mockResolvedValueOnce({ data: { keys: { total: 8, enabled: 7, disabled: 1, auth_invalid: 2, cooling_down: 3, ready: 2 }, active: 4, queue: { length: 5, capacity: 100 }, earliest_cooldown: '2026-07-30T10:00:00Z', shutting_down: false } })
      .mockRejectedValue(new ApiError(503, {
        type: 'server_error',
        code: 'summary_unavailable',
        message: '运行摘要暂时不可用。',
        param: null,
      }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')
    await flushPromises()

    // Save verdict is preserved; no "保存失败" form error is shown.
    expect(runtimeApi.updateSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="runtime-saved"]').text()).toContain('设置已保存')
    expect(wrapper.find('[data-testid="runtime-settings-error"]').exists()).toBe(false)
    // The summary refresh failure surfaces as a distinct alert, not a save error.
    expect(wrapper.get('[role="alert"]').text()).toContain('运行摘要暂时不可用')
    expect(wrapper.find('[data-testid="runtime-saved"]').exists()).toBe(true)
  })


  it('places a backend param validation error beside its field', async () => {
    vi.mocked(runtimeApi.updateSettings).mockRejectedValue(
      new ApiError(400, {
        type: 'invalid_request_error',
        code: 'invalid_setting',
        message: 'The runtime setting is outside its allowed range.',
        param: 'queue_wait_timeout_ms',
      }),
    )
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="error-queue_wait_timeout_ms"]').text()).toContain('allowed range')
    expect(wrapper.find('[data-testid="runtime-settings-error"]').exists()).toBe(false)
  })
})
