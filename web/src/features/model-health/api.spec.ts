import { describe, expect, it, vi } from 'vitest'

import { apiRequest } from '../../shared/api/client'
import { modelHealthApi } from './api'

vi.mock('../../shared/api/client', () => ({
  apiRequest: vi.fn(),
}))

describe('modelHealthApi', () => {
  it('encodes summary filters and sort parameters', async () => {
    vi.mocked(apiRequest).mockResolvedValue({ data: {} })
    const signal = new AbortController().signal

    await modelHealthApi.getSummary('6h', 'provider', 'availability', signal)

    expect(apiRequest).toHaveBeenCalledWith(
      '/admin/api/model-health/summary?range=6h&group=provider&sort=availability',
      { signal },
    )
  })

  it('patches detection settings and queues a manual run', async () => {
    vi.mocked(apiRequest).mockResolvedValue({ data: {} })

    await modelHealthApi.updateSettings({ enabled: true, interval_seconds: 300, concurrency: 4 })
    expect(apiRequest).toHaveBeenCalledWith(
      '/admin/api/model-health/settings',
      { method: 'PATCH', body: { enabled: true, interval_seconds: 300, concurrency: 4 } },
    )

    await modelHealthApi.runNow()
    expect(apiRequest).toHaveBeenCalledWith('/admin/api/model-health/run', { method: 'POST' })
  })
})
