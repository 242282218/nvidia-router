import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { accessKeysApi } from './api'
import AccessKeysView from './AccessKeysView.vue'

vi.mock('./api', () => ({
  accessKeysApi: {
    list: vi.fn(),
    create: vi.fn(),
    revoke: vi.fn(),
  },
}))

const listedKey = {
  id: 4,
  name: '家庭电脑',
  key_prefix: 'nvr_abcd',
  created_at: '2026-07-30T08:00:00Z',
  last_used_at: '2026-07-30T09:30:00Z',
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(accessKeysApi.list).mockResolvedValue({ data: [listedKey] })
  vi.mocked(accessKeysApi.revoke).mockResolvedValue(undefined)
})

describe('AccessKeysView', () => {
  it('shows a newly created plaintext once, copies it, and cannot recover it after closing', async () => {
    const plaintext = 'nvr_once_only_secret'
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    vi.mocked(accessKeysApi.create).mockResolvedValue({
      ...listedKey,
      id: 5,
      name: 'CI',
      key_prefix: 'nvr_ci12',
      key: plaintext,
    })
    const wrapper = mount(AccessKeysView)
    await flushPromises()

    await wrapper.get('[data-testid="open-create-access-key"]').trigger('click')
    await wrapper.get('[data-testid="access-key-name"]').setValue('CI')
    await wrapper.get('[data-testid="create-access-key-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="created-access-key"]').text()).toContain(plaintext)
    await wrapper.get('[data-testid="copy-created-access-key"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith(plaintext)

    await wrapper.get('[data-testid="close-created-access-key"]').trigger('click')
    expect(wrapper.text()).not.toContain(plaintext)
    expect(accessKeysApi.list).toHaveBeenCalledTimes(2)
    expect(JSON.stringify(vi.mocked(accessKeysApi.list).mock.results)).not.toContain(plaintext)

    await wrapper.get('[data-testid="open-create-access-key"]').trigger('click')
    expect(wrapper.findAll('[data-testid="created-access-key"]')).toHaveLength(0)
    expect(wrapper.findAll('[data-testid="create-access-key-form"]')).toHaveLength(1)
  })

  it('reports legacy copy failure when execCommand returns false', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn().mockReturnValue(false),
    })
    vi.mocked(accessKeysApi.create).mockResolvedValue({
      ...listedKey,
      id: 5,
      name: 'CI',
      key_prefix: 'nvr_ci12',
      key: 'nvr_legacy_secret',
    })
    const wrapper = mount(AccessKeysView)
    await flushPromises()

    await wrapper.get('[data-testid="open-create-access-key"]').trigger('click')
    await wrapper.get('[data-testid="access-key-name"]').setValue('CI')
    await wrapper.get('[data-testid="create-access-key-form"]').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid="copy-created-access-key"]').trigger('click')

    expect(wrapper.text()).toContain('复制失败，请手动复制。')
    expect(document.querySelector('textarea')).toBeNull()
  })
  it('shows safe metadata and requires confirmation before revoking', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const wrapper = mount(AccessKeysView)
    await flushPromises()

    expect(wrapper.text()).toContain('家庭电脑')
    expect(wrapper.text()).toContain('nvr_abcd')
    expect(wrapper.text()).toContain('2026/07/30')
    expect(wrapper.text()).toContain('09:30')

    const revoke = wrapper.get('[data-testid="revoke-access-key-4"]')
    await revoke.trigger('click')
    expect(accessKeysApi.revoke).not.toHaveBeenCalled()
    await revoke.trigger('click')
    await flushPromises()

    expect(confirm).toHaveBeenCalledTimes(2)
    expect(accessKeysApi.revoke).toHaveBeenCalledWith(4)
  })
})
