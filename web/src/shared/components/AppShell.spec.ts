import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AppShell from './AppShell.vue'
import { authApi } from '../../features/auth/api'
import { createSessionStore, sessionKey } from '../../features/auth/useSession'
import { createAppRouter } from '../../router'

vi.mock('../../features/auth/api', () => ({
  authApi: {
    changePassword: vi.fn(),
    getSession: vi.fn().mockResolvedValue({ authenticated: true, must_change_password: false }),
    login: vi.fn(),
    logout: vi.fn().mockResolvedValue(undefined),
  },
}))

vi.mock('../../features/nvidia-keys/api', () => ({
  nvidiaKeysApi: { list: vi.fn().mockResolvedValue({ data: [] }) },
}))

vi.mock('../../features/models/api', () => ({
  modelsApi: { list: vi.fn().mockResolvedValue({ data: [] }) },
}))

vi.mock('../../features/access-keys/api', () => ({
  accessKeysApi: { list: vi.fn().mockResolvedValue({ data: [] }) },
}))

vi.mock('../../features/runtime/api', () => ({
  runtimeApi: {
    getSummary: vi.fn().mockResolvedValue({
      data: { keys: { total: 0, enabled: 0, disabled: 0, auth_invalid: 0, cooling_down: 0, ready: 0 }, active: 0, queue: { length: 0, capacity: 100 }, shutting_down: false },
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
  },
}))

vi.mock('../../features/statistics/api', () => ({
  statisticsApi: {
    getSummary: vi.fn().mockResolvedValue({ data: { range: '24h', from: '', to: '', summary: { request_count: 0, success_count: 0, failure_count: 0, success_rate: 0, average_duration_ms: 0, average_first_byte_ms: 0, average_queue_ms: 0, total_attempts: 0, prompt_tokens: 0, completion_tokens: 0 }, series: [] } }),
    getLogs: vi.fn().mockResolvedValue({ data: { items: [], page: 1, page_size: 50, total: 0, has_more: false } }),
  },
}))

vi.mock('../../features/proxy/api', () => ({
  proxyPoolApi: {
    get: vi.fn().mockResolvedValue({ data: { enabled: false, proxy_url: '', auth_configured: false, source: 'none' } }),
    update: vi.fn(),
  },
}))

function makeMatchMedia(matches: boolean): () => MediaQueryList {
  return () => ({
    matches,
    media: '',
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  } as unknown as MediaQueryList)
}

async function mountShell(mobile: boolean) {
  const stub = makeMatchMedia(mobile)
  vi.stubGlobal('matchMedia', stub)
  const session = createSessionStore(authApi)
  const router = createAppRouter(session, createMemoryHistory('/admin/'))
  await router.push('/nvidia-keys')
  await router.isReady()
  const wrapper = mount(AppShell, {
    attachTo: document.body,
    global: {
      plugins: [router],
      provide: { [sessionKey as symbol]: session },
    },
  })
  await flushPromises()
  return wrapper
}

describe('AppShell mobile drawer focus management', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps the sidebar inert when the mobile drawer is closed', async () => {
    const wrapper = await mountShell(true)
    const sidebar = wrapper.get('#admin-sidebar')
    expect(sidebar.attributes('inert')).toBeDefined()

    await wrapper.get('[aria-label="切换菜单"]').trigger('click')
    expect(sidebar.attributes('inert')).toBeUndefined()
  })

  it('uses one mobile drawer translation state at a time', async () => {
    const wrapper = await mountShell(true)
    const sidebar = wrapper.get('#admin-sidebar')

    expect(sidebar.classes()).toContain('-translate-x-full')
    await wrapper.get('[aria-label="切换菜单"]').trigger('click')

    expect(sidebar.classes()).toContain('translate-x-0')
    expect(sidebar.classes()).not.toContain('-translate-x-full')
  })

  it('keeps the mobile header above the open drawer for the close control', async () => {
    const wrapper = await mountShell(true)
    const header = wrapper.get('header')

    await wrapper.get('[aria-label="切换菜单"]').trigger('click')

    expect(header.classes()).toContain('z-[var(--z-toolbar)]')
  })

  it('never applies inert on desktop where the rail is always visible', async () => {
    const wrapper = await mountShell(false)
    expect(wrapper.get('#admin-sidebar').attributes('inert')).toBeUndefined()
  })

  it('moves focus into the drawer on open and back to the menu button on close', async () => {
    const wrapper = await mountShell(true)
    const menuButton = wrapper.get('[aria-label="切换菜单"]').element as HTMLButtonElement
    menuButton.focus()

    await wrapper.get('[aria-label="切换菜单"]').trigger('click')
    await flushPromises()
    const firstLink = wrapper.get('nav a').element
    expect(document.activeElement).toBe(firstLink)

    await wrapper.get('[aria-label="切换菜单"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(menuButton)
  })

  it('closes the mobile drawer with Escape and restores focus', async () => {
    const wrapper = await mountShell(true)
    const menuButton = wrapper.get('[aria-label="切换菜单"]').element as HTMLButtonElement
    await wrapper.get('[aria-label="切换菜单"]').trigger('click')
    await flushPromises()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()

    expect(wrapper.get('#admin-sidebar').attributes('inert')).toBeDefined()
    expect(document.activeElement).toBe(menuButton)
  })
})

describe('AppShell sidebar chrome', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // 快捷键提示必须与 useHotkeys.formatCombo 同源（Ctrl 在所有平台都生效），
  // 防止再次出现侧栏 ⌘K 与命令面板 Ctrl K 两套口径。
  it('derives the search hotkey hint from formatCombo instead of a hardcoded glyph', async () => {
    const wrapper = await mountShell(false)
    const entry = wrapper.get('[data-testid="open-command-palette"]')
    expect(entry.text()).toContain('Ctrl K')
    expect(entry.attributes('title')).toBe('搜索（Ctrl K）')
  })

  it('renders the account identity with an inline status, not a dedicated line', async () => {
    const wrapper = await mountShell(false)
    const aside = wrapper.get('#admin-sidebar')
    expect(aside.text()).toContain('管理员')
    expect(aside.find('[aria-label="管理员，会话有效"]').exists()).toBe(true)
  })

  it('uses a flat brand lockup and a restrained rail toggle', async () => {
    const wrapper = await mountShell(false)
    const brand = wrapper.get('[data-testid="sidebar-brand"]')
    const toggle = wrapper.get('[data-testid="toggle-rail"]')

    expect(brand.classes()).toEqual(expect.arrayContaining(['sidebar-brand']))
    expect(brand.find('[data-testid="sidebar-brand-mark"]').classes()).toEqual(
      expect.arrayContaining(['sidebar-brand-mark']),
    )
    expect(toggle.classes()).toEqual(expect.arrayContaining(['sidebar-rail-toggle']))
  })
})
