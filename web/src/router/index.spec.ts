import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

import App from '../App.vue'
import type { AuthApi } from '../features/auth/api'
import type { SessionStore, SessionState } from '../features/auth/useSession'
import { createSessionStore, sessionKey } from '../features/auth/useSession'
import { apiRequest } from '../shared/api/client'
import { createAppRouter } from './index'

vi.mock('../features/nvidia-keys/api', () => ({
  nvidiaKeysApi: {
    list: vi.fn().mockResolvedValue({ data: [] }),
    importOne: vi.fn(),
    importBatch: vi.fn(),
    test: vi.fn(),
    testAll: vi.fn(),
    setEnabled: vi.fn(),
    remove: vi.fn(),
  },
}))

vi.mock('../features/models/api', () => ({
  modelsApi: {
    list: vi.fn().mockResolvedValue({ data: [] }),
    candidates: vi.fn(),
    save: vi.fn(),
    patch: vi.fn(),
    unblock: vi.fn(),
  },
}))

vi.mock('../features/access-keys/api', () => ({
  accessKeysApi: {
    list: vi.fn().mockResolvedValue({ data: [] }),
    create: vi.fn(),
    revoke: vi.fn(),
  },
}))

vi.mock('../features/runtime/api', () => ({
  runtimeApi: {
    getSummary: vi.fn().mockResolvedValue({
      data: {
        keys: { total: 0, enabled: 0, disabled: 0, auth_invalid: 0, cooling_down: 0, ready: 0 },
        active: 0,
        queue: { length: 0, capacity: 100 },
        shutting_down: false,
      },
    }),
    getSettings: vi.fn().mockResolvedValue({
      data: {
        queue_capacity: 100,
        queue_wait_timeout_ms: 60000,
        connect_timeout_ms: 10000,
        first_byte_timeout_ms: 60000,
        nonstream_total_timeout_ms: 300000,
        shutdown_grace_ms: 60000,
      },
    }),
    updateSettings: vi.fn(),
  },
}))

vi.mock('../features/statistics/api', () => ({
  statisticsApi: {
    getDaily: vi.fn().mockResolvedValue({ data: [] }),
    getRecentErrors: vi.fn().mockResolvedValue({ data: [] }),
  },
}))

function createSession(state: SessionState): SessionStore {
  return {
    changePassword: vi.fn(),
    dispose: vi.fn(),
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

  it('redirects an authenticated management page after any API returns 401 and cleans up on unmount', async () => {
    const api: AuthApi = {
      changePassword: vi.fn(),
      getSession: vi.fn().mockResolvedValue({ authenticated: true, must_change_password: false }),
      login: vi.fn(),
      logout: vi.fn(),
    }
    const session = createSessionStore(api)
    const router = createAppRouter(session, createMemoryHistory('/admin/'))
    const wrapper = mount(App, {
      global: {
        plugins: [router],
        provide: { [sessionKey as symbol]: session },
      },
    })
    let dispose: ReturnType<typeof vi.spyOn> | undefined

    try {
      await router.push('/models')
      await router.isReady()
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

      await expect(apiRequest('/admin/api/models')).rejects.toMatchObject({ status: 401 })
      await flushPromises()

      expect(router.currentRoute.value.path).toBe('/login')
      expect(session.dispose).toBeTypeOf('function')
      dispose = vi.spyOn(session, 'dispose')
    } finally {
      wrapper.unmount()
      vi.unstubAllGlobals()
    }
    expect(dispose).toHaveBeenCalledOnce()
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

  it.each([
    ['nav-nvidia-keys', '/nvidia-keys', 'NVIDIA Key'],
    ['nav-models', '/models', '模型白名单'],
    ['nav-access-keys', '/access-keys', 'Access Key'],
    ['nav-runtime', '/runtime', '运行状态'],
    ['nav-statistics', '/statistics', '基础统计'],
  ])('navigates from AppShell through %s', async (testId, expectedPath, expectedHeading) => {
    const session = createSession({ kind: 'authenticated', mustChangePassword: false })
    const router = createAppRouter(session, createMemoryHistory('/admin/'))
    const wrapper = mount(App, {
      global: {
        plugins: [router],
        provide: { [sessionKey as symbol]: session },
      },
    })

    await router.push('/')
    await router.isReady()
    await wrapper.get(`[data-testid="${testId}"]`).trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe(expectedPath)
    expect(wrapper.get('h1').text()).toBe(expectedHeading)
  })
})

describe('management routes', () => {
  it('keeps task 36 routes and registers task 37 routes', () => {
    const router = createAppRouter(
      createSession({ kind: 'authenticated', mustChangePassword: false }),
      createMemoryHistory('/admin/'),
    )
    const paths = router.getRoutes().map((route) => route.path)

    expect(paths).toContain('/nvidia-keys')
    expect(paths).toContain('/models')
    expect(paths).toContain('/access-keys')
    expect(paths).toContain('/runtime')
    expect(paths).toContain('/statistics')
  })
})
