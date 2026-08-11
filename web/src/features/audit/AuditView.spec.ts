import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { auditApi } from './api'
import AuditView from './AuditView.vue'
import type { AuditEntry } from './types'

vi.mock('./api', () => ({
  auditApi: {
    list: vi.fn(),
  },
}))

const entry: AuditEntry = {
  id: 9,
  action: 'nvidia_keys.import',
  target_type: 'nvidia-keys',
  target_id: '3',
  detail: '{"imported":1}',
  client_ip: '10.0.0.1',
  created_at: '2026-08-11T08:00:00Z',
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('AuditView', () => {
  it('renders audit entries from the API', async () => {
    vi.mocked(auditApi.list).mockResolvedValue({ data: { items: [entry], total: 1, has_more: false } })
    const wrapper = mount(AuditView)
    await flushPromises()

    expect(wrapper.text()).toContain('nvidia_keys.import')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('10.0.0.1')
  })

  it('shows an empty state when there are no entries', async () => {
    vi.mocked(auditApi.list).mockResolvedValue({ data: { items: [], total: 0, has_more: false } })
    const wrapper = mount(AuditView)
    await flushPromises()

    expect(wrapper.text()).toContain('暂无审计记录')
  })

  it('paginates forward via the next button', async () => {
    vi.mocked(auditApi.list)
      .mockResolvedValueOnce({ data: { items: [entry], total: 20, has_more: true, next: 2 } })
      .mockResolvedValueOnce({ data: { items: [], total: 20, has_more: false } })

    const wrapper = mount(AuditView)
    await flushPromises()
    await wrapper.get('[data-testid="audit-next"]').trigger('click')
    await flushPromises()

    expect(vi.mocked(auditApi.list).mock.calls[1]?.[0]?.page).toBe(2)
    expect(wrapper.text()).toContain('第 2 页')
  })

  it('filters by action on select change', async () => {
    vi.mocked(auditApi.list).mockResolvedValue({ data: { items: [], total: 0, has_more: false } })
    const wrapper = mount(AuditView)
    await flushPromises()

    const select = wrapper.get('[data-testid="audit-action-filter"]')
    await select.setValue('access_keys.revoke')
    await flushPromises()

    const lastCall = vi.mocked(auditApi.list).mock.calls.at(-1)?.[0]
    expect(lastCall?.action).toBe('access_keys.revoke')
    expect(lastCall?.page).toBe(1)
  })
})
