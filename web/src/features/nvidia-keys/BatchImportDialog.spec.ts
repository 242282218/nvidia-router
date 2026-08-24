import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { nvidiaKeysApi } from './api'
import BatchImportDialog from './BatchImportDialog.vue'
import type { BatchImportResponse } from './types'

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

describe('BatchImportDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('shows safe line results, clears immediately, and notifies the parent after success', async () => {
    let resolveRequest: ((value: BatchImportResponse) => void) | undefined
    vi.mocked(nvidiaKeysApi.importBatch).mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve
      }) as ReturnType<typeof nvidiaKeysApi.importBatch>,
    )
    const wrapper = mount(BatchImportDialog, {
      props: { open: true },
      global: { stubs: { Teleport: true } },
    })
    const secret = 'nvapi-secret-that-must-not-remain'

    await wrapper.get('textarea').setValue(`first\n${secret}`)
    await wrapper.get('form').trigger('submit')

    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.html()).not.toContain(secret)

    resolveRequest?.({
      data: [
        { line: 1, masked: 'nvapi…first', status: 'invalid', reason: 'invalid format' },
        { line: 2, masked: 'nvapi…main', status: 'imported' },
      ],
    })
    await flushPromises()

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0]?.text()).toContain('1')
    expect(rows[0]?.text()).toContain('nvapi…first')
    expect(rows[0]?.text()).toContain('invalid')
    expect(rows[0]?.text()).toContain('invalid format')
    expect(rows[1]?.text()).toContain('imported')
    expect(wrapper.emitted('imported')).toHaveLength(1)
    expect(wrapper.html()).not.toContain(secret)
    expect(nvidiaKeysApi.test).not.toHaveBeenCalled()
    expect(nvidiaKeysApi.testAll).not.toHaveBeenCalled()
    expect(wrapper.get('button[type="submit"]').text()).toContain('导入')
  })

  it('keeps import loading separate from key testing', async () => {
    let resolveRequest: ((value: BatchImportResponse) => void) | undefined
    vi.mocked(nvidiaKeysApi.importBatch).mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve
      }) as ReturnType<typeof nvidiaKeysApi.importBatch>,
    )
    const wrapper = mount(BatchImportDialog, {
      props: { open: true },
      global: { stubs: { Teleport: true } },
    })

    await wrapper.get('textarea').setValue('valid-token-123456')
    await wrapper.get('form').trigger('submit')

    const submitButton = wrapper.get('button[type="submit"]')
    expect(submitButton.attributes('disabled')).toBeDefined()
    expect(submitButton.text()).toContain('导入中')
    expect(nvidiaKeysApi.test).not.toHaveBeenCalled()
    expect(nvidiaKeysApi.testAll).not.toHaveBeenCalled()

    resolveRequest?.({ data: [] })
    await flushPromises()
    expect(submitButton.attributes('disabled')).toBeUndefined()
  })

  it('runs upstream probes for newly imported keys when requested', async () => {
    const importedId = 7
    vi.mocked(nvidiaKeysApi.importBatch).mockResolvedValue({
      data: [
        { line: 1, masked: 'nvapi…k1', status: 'imported', key: { id: importedId, masked: 'nvapi…k1', enabled: true, auth_invalid: false, cooldown_level: 0, consecutive_failures: 0, created_at: '2026-08-11T00:00:00Z', updated_at: '2026-08-11T00:00:00Z' } },
        { line: 2, masked: 'nvapi…dup', status: 'duplicate' },
      ],
    })
    vi.mocked(nvidiaKeysApi.test).mockResolvedValue({ id: importedId, status: 'valid' })

    const wrapper = mount(BatchImportDialog, {
      props: { open: true },
      global: { stubs: { Teleport: true } },
    })
    await wrapper.get('textarea').setValue('nvapi-k1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    // Button only appears when at least one key was actually imported.
    const probe = wrapper.get('[data-testid="test-newly-imported"]')
    expect(probe.text()).toContain('测活新增 1 个')
    await probe.trigger('click')
    await flushPromises()

    expect(nvidiaKeysApi.test).toHaveBeenCalledWith(importedId)
    expect(wrapper.get('[data-testid="batch-import-test-results"]').text()).toContain('valid')
  })

  it('does not submit empty input', async () => {
    const wrapper = mount(BatchImportDialog, {
      props: { open: true },
      global: { stubs: { Teleport: true } },
    })

    await wrapper.get('form').trigger('submit')

    expect(nvidiaKeysApi.importBatch).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('至少一行')
  })
})
