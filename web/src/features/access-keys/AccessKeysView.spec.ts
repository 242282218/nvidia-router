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

  it('clears the temporary textarea after a successful legacy copy and keeps plaintext visible', async () => {
    const plaintext = 'nvr_legacy_success_secret'
    let textarea: HTMLTextAreaElement | undefined
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn().mockImplementation(() => {
        const activeTextarea = document.querySelector('textarea') as HTMLTextAreaElement
        textarea = activeTextarea
        expect(activeTextarea.value).toBe(plaintext)
        expect(document.body.contains(activeTextarea)).toBe(true)
        return true
      }),
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
    await wrapper.get('[data-testid="copy-created-access-key"]').trigger('click')

    expect(textarea?.value).toBe('')
    expect(document.querySelector('textarea')).toBeNull()
    expect(wrapper.get('[data-testid="created-access-key"]').text()).toContain(plaintext)
    expect(wrapper.text()).toContain('已复制。')
  })

  it('clears the temporary textarea after a failed legacy copy and keeps plaintext visible', async () => {
    const plaintext = 'nvr_legacy_false_secret'
    let textarea: HTMLTextAreaElement | undefined
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn().mockImplementation(() => {
        textarea = document.querySelector('textarea') as HTMLTextAreaElement
        return false
      }),
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
    await wrapper.get('[data-testid="copy-created-access-key"]').trigger('click')

    expect(textarea?.value).toBe('')
    expect(document.querySelector('textarea')).toBeNull()
    expect(wrapper.get('[data-testid="created-access-key"]').text()).toContain(plaintext)
    expect(wrapper.text()).toContain('复制失败，请手动复制。')
  })

  it('clears the temporary textarea after a throwing legacy copy and keeps plaintext visible', async () => {
    const plaintext = 'nvr_legacy_throw_secret'
    let textarea: HTMLTextAreaElement | undefined
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn().mockImplementation(() => {
        textarea = document.querySelector('textarea') as HTMLTextAreaElement
        throw new Error('copy failed')
      }),
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
    await wrapper.get('[data-testid="copy-created-access-key"]').trigger('click')

    expect(textarea?.value).toBe('')
    expect(document.querySelector('textarea')).toBeNull()
    expect(wrapper.get('[data-testid="created-access-key"]').text()).toContain(plaintext)
    expect(wrapper.text()).toContain('复制失败，请手动复制。')
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
