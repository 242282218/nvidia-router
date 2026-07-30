import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/client'
import type { AuthApi } from './api'
import ChangePasswordView from './ChangePasswordView.vue'
import { createSessionStore, sessionKey } from './useSession'

const PlaceholderView = defineComponent({ template: '<div>placeholder</div>' })

function createAuthApi(): AuthApi {
  return {
    changePassword: vi.fn(),
    getSession: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
  }
}

async function mountChangePassword(api: AuthApi) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { component: PlaceholderView, path: '/' },
      { component: PlaceholderView, path: '/login' },
      { component: ChangePasswordView, path: '/change-password' },
    ],
  })
  await router.push('/change-password')
  await router.isReady()
  const wrapper = mount(ChangePasswordView, {
    global: {
      plugins: [router],
      provide: { [sessionKey as symbol]: createSessionStore(api) },
    },
  })
  return { router, wrapper }
}

describe('ChangePasswordView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the plaintext HTTP risk banner', async () => {
    const { wrapper } = await mountChangePassword(createAuthApi())

    expect(wrapper.get('[role="alert"]').text()).toContain('HTTP')
    expect(wrapper.get('[role="alert"]').text()).toContain('明文')
  })

  it.each([
    ['short-pass', '新密码至少需要 12 个字符。'],
    ['admin', '新密码不能为 admin。'],
  ])('rejects invalid new password %s before sending it', async (password, expectedMessage) => {
    const api = createAuthApi()
    const { wrapper } = await mountChangePassword(api)

    await wrapper.get('[name="current-password"]').setValue('admin')
    await wrapper.get('[name="new-password"]').setValue(password)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.changePassword).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="form-error"]').text()).toBe(expectedMessage)
  })

  it('changes a valid password and routes into the app', async () => {
    const api = createAuthApi()
    vi.mocked(api.changePassword).mockResolvedValue({ authenticated: true, must_change_password: false })
    const { router, wrapper } = await mountChangePassword(api)

    await wrapper.get('[name="current-password"]').setValue('admin')
    await wrapper.get('[name="new-password"]').setValue('replacement-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.changePassword).toHaveBeenCalledWith('admin', 'replacement-password')
    expect(router.currentRoute.value.path).toBe('/')
  })

  it('shows a safe error without echoing either submitted password', async () => {
    const api = createAuthApi()
    vi.mocked(api.changePassword).mockRejectedValue(
      new ApiError(400, {
        code: 'invalid_request',
        message: 'The new password does not meet the password policy.',
        param: null,
        type: 'invalid_request_error',
      }),
    )
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const { wrapper } = await mountChangePassword(api)

    await wrapper.get('[name="current-password"]').setValue('current-secret')
    await wrapper.get('[name="new-password"]').setValue('replacement-secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="form-error"]').text()).toBe(
      'The new password does not meet the password policy.',
    )
    expect(wrapper.text()).not.toContain('current-secret')
    expect(wrapper.text()).not.toContain('replacement-secret')
    expect(storageWrite).not.toHaveBeenCalled()
  })
})


