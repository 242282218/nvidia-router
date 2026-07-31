import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { nvidiaKeysApi } from './api'
import NvidiaKeysView from './NvidiaKeysView.vue'
import type { NVIDIAKey, SingleImportResponse } from './types'

vi.mock('./api', () => ({
  nvidiaKeysApi: {
    list: vi.fn(),
    importOne: vi.fn(),
    importBatch: vi.fn(),
    test: vi.fn(),
    testAll: vi.fn(),
    setEnabled: vi.fn(),
    remove: vi.fn(),
  },
}))

function makeKey(overrides: Partial<NVIDIAKey> = {}): NVIDIAKey {
  return {
    id: 7,
    masked: 'nvapi…1234',
    enabled: true,
    auth_invalid: false,
    cooldown_level: 0,
    consecutive_failures: 0,
    created_at: '2026-07-30T00:00:00Z',
    updated_at: '2026-07-30T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(nvidiaKeysApi.list).mockResolvedValue({ data: [] })
})

describe('NvidiaKeysView', () => {
  it('renders responsive key views with cooldown and recent error metadata, without secret actions', async () => {
    vi.mocked(nvidiaKeysApi.list).mockResolvedValue({
      data: [makeKey({
        cooldown_until: '2026-07-30T01:00:00Z',
        cooldown_reason: 'rate_limited',
        consecutive_failures: 2,
        last_error_code: 'upstream_rate_limited',
        last_error_at: '2026-07-30T00:30:00Z',
      })],
    })
    const wrapper = mount(NvidiaKeysView)
    await flushPromises()

    const table = wrapper.get('[data-testid="key-table"]')
    const cards = wrapper.get('[data-testid="key-cards"]')
    expect(table.classes()).toEqual(expect.arrayContaining(['hidden', 'md:block']))
    expect(cards.classes()).toEqual(expect.arrayContaining(['md:hidden']))
    expect(table.text()).toContain('nvapi…1234')
    expect(table.text()).toContain('2026-07-30T01:00:00Z')
    expect(cards.text()).toContain('upstream_rate_limited')
    expect(cards.text()).toContain('2026-07-30T00:30:00Z')
    expect(wrapper.text()).not.toMatch(/复制|查看明文|导出/)
  })

  it('clears the single import secret before the request settles and never restores it to the DOM', async () => {
    let resolveRequest: ((value: SingleImportResponse) => void) | undefined
    vi.mocked(nvidiaKeysApi.importOne).mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve
      }) as ReturnType<typeof nvidiaKeysApi.importOne>,
    )
    const wrapper = mount(NvidiaKeysView)
    await flushPromises()

    const secret = 'nvapi-fixture-not-a-real-key-123456789'
    await wrapper.get('[name="nvidia-key"]').setValue(secret)
    await wrapper.get('[data-testid="single-import-form"]').trigger('submit')

    expect((wrapper.get('[name="nvidia-key"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.html()).not.toContain(secret)
    expect(nvidiaKeysApi.importOne).toHaveBeenCalledWith(secret)

    resolveRequest?.({ line: 1, masked: 'nvapi…main', status: 'imported' })
    await flushPromises()

    expect(wrapper.text()).toContain('nvapi…main')
    expect(wrapper.html()).not.toContain(secret)
  })

  it('supports mobile enable, manual test and delete actions and shows the desktop batch hint', async () => {
    const key = makeKey({ id: 8, masked: 'nvapi…5678' })
    vi.mocked(nvidiaKeysApi.list).mockResolvedValue({ data: [key] })
    vi.mocked(nvidiaKeysApi.setEnabled).mockResolvedValue({ id: 8, enabled: false })
    vi.mocked(nvidiaKeysApi.test).mockResolvedValue({ id: 8, status: 'ok' })
    vi.mocked(nvidiaKeysApi.remove).mockResolvedValue(undefined)
    const wrapper = mount(NvidiaKeysView)
    await flushPromises()

    expect(wrapper.get('[data-testid="mobile-batch-hint"]').text()).toContain('桌面端')
    await wrapper.get('[data-testid="key-card-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="key-card-test"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="key-card-delete"]').trigger('click')
    await flushPromises()

    expect(nvidiaKeysApi.setEnabled).toHaveBeenCalledWith(8, false)
    expect(nvidiaKeysApi.test).toHaveBeenCalledWith(8)
    expect(nvidiaKeysApi.remove).toHaveBeenCalledWith(8)
  })

  it('shows every result returned by the sequential test-all action', async () => {
    vi.mocked(nvidiaKeysApi.list).mockResolvedValue({ data: [makeKey(), makeKey({ id: 8 })] })
    vi.mocked(nvidiaKeysApi.testAll).mockResolvedValue({
      data: [
        { id: 7, status: 'ok', models: ['vendor/chat'] },
        { id: 8, status: 'invalid', reason: 'authentication failed' },
      ],
    })
    const wrapper = mount(NvidiaKeysView)
    await flushPromises()

    await wrapper.get('[data-testid="test-all-keys"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-testid="key-test-results"]')
    expect(dialog.text()).toContain('#7')
    expect(dialog.text()).toContain('#8')
    expect(dialog.text()).toContain('authentication failed')
  })
})
