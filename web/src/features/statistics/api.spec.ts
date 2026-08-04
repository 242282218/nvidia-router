import { describe, expect, it, vi } from 'vitest'

import { apiRequest } from '../../shared/api/client'
import { statisticsApi } from './api'

vi.mock('../../shared/api/client', () => ({
  apiRequest: vi.fn(),
}))

describe('statisticsApi monitoring queries', () => {
  it('encodes non-empty summary filters with URLSearchParams', async () => {
    vi.mocked(apiRequest).mockResolvedValue({ data: {} })
    const signal = new AbortController().signal

    await statisticsApi.getSummary('7d', {
      search: 'model/a&b',
      model_id: 'model/a',
      endpoint: '/v1/chat/completions',
      outcome: 'failure',
      status: 502,
      access_key_id: 4,
      nvidia_key_id: 7,
    }, signal)

    expect(apiRequest).toHaveBeenCalledWith(
      '/admin/api/monitoring/summary?range=7d&search=model%2Fa%26b&model_id=model%2Fa&endpoint=%2Fv1%2Fchat%2Fcompletions&outcome=failure&status=502&access_key_id=4&nvidia_key_id=7',
      { signal },
    )
  })

  it('sends page parameters and omits empty filters for logs', async () => {
    vi.mocked(apiRequest).mockResolvedValue({ data: {} })

    await statisticsApi.getLogs('24h', { search: 'safe' }, 2, 20)

    expect(apiRequest).toHaveBeenCalledWith(
      '/admin/api/monitoring/logs?range=24h&search=safe&page=2&page_size=20',
      { signal: undefined },
    )
  })
})
