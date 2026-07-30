import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/client'
import type { AuthApi } from './api'
import LoginView from './LoginView.vue'
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

async function mountLogin(api: AuthApi) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { component: PlaceholderView, path: '/' },
      { component: LoginView, path: '/login' },
      { component: PlaceholderView, path: '/change-password' },
    ],
  })
  await router.push('/login')
  await router.isReady()
  const store = createSessionStore(api)
  const wrapper = mount(LoginView, {
    global: {
      plugins: [router],
      provide: { [sessionKey as symbol]: store },
    },
  })
  return { router, wrapper }
}

describe('LoginView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the plaintext HTTP risk banner', async () => {
    const { wrapper } = await mountLogin(createAuthApi())

    expect(wrapper.get('[role="alert"]').text()).toContain('HTTP')
    expect(wrapper.get('[role="alert"]').text()).toContain('明文')
  })

  it('logs in and routes a restricted session to password change', async () => {
    const api = createAuthApi()
    vi.mocked(api.login).mockResolvedValue({ authenticated: true, must_change_password: true })
    const { router, wrapper } = await mountLogin(api)

    await wrapper.get('[name="username"]').setValue('admin')
    await wrapper.get('[name="password"]').setValue('admin')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.login).toHaveBeenCalledWith('admin', 'admin')
    expect(router.currentRoute.value.path).toBe('/change-password')
  })

  it('shows a safe API error without echoing the submitted password', async () => {
    const api = createAuthApi()
    vi.mocked(api.login).mockRejectedValue(
      new ApiError(401, {
        code: 'invalid_credentials',
        message: 'The username or password is incorrect.',
        param: null,
        type: 'authentication_error',
      }),
    )
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const { wrapper } = await mountLogin(api)

    await wrapper.get('[name="password"]').setValue('submitted-secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('[data-testid="form-error"]').text()).toBe('The username or password is incorrect.')
    expect(wrapper.text()).not.toContain('submitted-secret')
    expect(storageWrite).not.toHaveBeenCalled()
  })
})


