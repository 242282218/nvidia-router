import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/client'
import { runtimeApi } from './api'
import RuntimeView from './RuntimeView.vue'

vi.mock('./api', () => ({
  runtimeApi: {
    getSummary: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
  },
}))

const settings = {
  queue_capacity: 100,
  queue_wait_timeout_ms: 60_000,
  connect_timeout_ms: 10_000,
  first_byte_timeout_ms: 60_000,
  nonstream_total_timeout_ms: 300_000,
  shutdown_grace_ms: 60_000,
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
})

describe('RuntimeView', () => {
  it('shows key, active, queue, cooldown and shutdown state', async () => {
    const wrapper = mount(RuntimeView)
    await flushPromises()

    expect(wrapper.get('[data-testid="runtime-key-counts"]').text()).toContain('总数 8')
    expect(wrapper.get('[data-testid="runtime-key-counts"]').text()).toContain('冷却 3')
    expect(wrapper.get('[data-testid="runtime-active"]').text()).toContain('4')
    expect(wrapper.get('[data-testid="runtime-queue"]').text()).toContain('5 / 100')
    expect(wrapper.get('[data-testid="runtime-cooldown"]').text()).toContain('2026/07/30')
    expect(wrapper.get('[data-testid="runtime-shutdown"]').text()).toContain('关闭中')
  })

  it('edits all settings in UI units and converts them to backend milliseconds', async () => {
    const wrapper = mount(RuntimeView)
    await flushPromises()

    expect((wrapper.get('[data-testid="queue-capacity"]').element as HTMLInputElement).value).toBe('100')
    expect((wrapper.get('[data-testid="queue-wait-seconds"]').element as HTMLInputElement).value).toBe('60')
    expect((wrapper.get('[data-testid="connect-timeout-seconds"]').element as HTMLInputElement).value).toBe('10')
    expect((wrapper.get('[data-testid="first-byte-timeout-seconds"]').element as HTMLInputElement).value).toBe('60')
    expect((wrapper.get('[data-testid="nonstream-timeout-minutes"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('[data-testid="shutdown-grace-seconds"]').element as HTMLInputElement).value).toBe('60')

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')
    await flushPromises()

    expect(runtimeApi.updateSettings).toHaveBeenCalledWith(settings)
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
    const wrapper = mount(RuntimeView)
    await flushPromises()

    await wrapper.get('[data-testid="runtime-settings-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="error-queue_wait_timeout_ms"]').text()).toContain('allowed range')
    expect(wrapper.find('[data-testid="runtime-settings-error"]').exists()).toBe(false)
  })
})
