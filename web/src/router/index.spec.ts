import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

import App from '../App.vue'
import type { SessionStore, SessionState } from '../features/auth/useSession'
import { sessionKey } from '../features/auth/useSession'
import { createAppRouter } from './index'

function createSession(state: SessionState): SessionStore {
  return {
    changePassword: vi.fn(),
    ensureLoaded: vi.fn().mockResolvedValue(undefined),
    login: vi.fn(),
    logout: vi.fn(),
    refresh: vi.fn(),
    state: ref(state),
  }
}

async function navigate(state: SessionState, target = '/') {
  const session = createSession(state)
  const router = createAppRouter(session, createMemoryHistory('/admin/'))
  await router.push(target)
  await router.isReady()
  return { router, session }
}

describe('authentication router guard', () => {
  it('redirects an anonymous visitor to login', async () => {
    const { router } = await navigate({ kind: 'anonymous' })

    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('allows a forced-change session to visit only password change', async () => {
    const { router } = await navigate({ kind: 'authenticated', mustChangePassword: true })

    expect(router.currentRoute.value.path).toBe('/change-password')
    await router.push('/login')
    expect(router.currentRoute.value.path).toBe('/change-password')
  })

  it('routes a normal session into AppShell', async () => {
    const { router } = await navigate({ kind: 'authenticated', mustChangePassword: false })
    const matchedComponent = router.currentRoute.value.matched.at(-1)?.components?.default

    expect(router.currentRoute.value.path).toBe('/')
    expect((matchedComponent as { name?: string }).name).toBe('AppShell')
  })
})

describe('application router integration', () => {
  it.each([
    [{ kind: 'anonymous' } as SessionState, '/login', '管理员登录'],
    [
      { kind: 'authenticated', mustChangePassword: true } as SessionState,
      '/change-password',
      '修改管理员密码',
    ],
  ])('renders the guarded entry route at %s', async (state, expectedPath, expectedHeading) => {
    const session = createSession(state)
    const router = createAppRouter(session, createMemoryHistory('/admin/'))
    const wrapper = mount(App, {
      global: {
        plugins: [router],
        provide: { [sessionKey as symbol]: session },
      },
    })

    await router.push('/')
    await router.isReady()
    await flushPromises()

    expect(router.currentRoute.value.path).toBe(expectedPath)
    expect(wrapper.get('h1').text()).toBe(expectedHeading)
  })
})
