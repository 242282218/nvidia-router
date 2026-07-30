import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

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
  it('shows safe line results, clears immediately, and notifies the parent after success', async () => {
    let resolveRequest: ((value: BatchImportResponse) => void) | undefined
    vi.mocked(nvidiaKeysApi.importBatch).mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve
      }) as ReturnType<typeof nvidiaKeysApi.importBatch>,
    )
    const wrapper = mount(BatchImportDialog, { props: { open: true } })
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
  })
})
