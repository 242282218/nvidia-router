import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AppCommandPalette from './AppCommandPalette.vue'
import { authApi } from '../../features/auth/api'
import { createSessionStore, sessionKey } from '../../features/auth/useSession'
import { createAppRouter } from '../../router'
import { useCommandPalette } from '../useCommandPalette'

vi.mock('../../features/auth/api', () => ({
  authApi: {
    changePassword: vi.fn(),
    getSession: vi.fn().mockResolvedValue({ authenticated: true, must_change_password: false }),
    login: vi.fn(),
    logout: vi.fn().mockResolvedValue(undefined),
  },
}))

function makeMatchMedia(): () => MediaQueryList {
  return () => ({
    matches: false,
    media: '',
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  } as unknown as MediaQueryList)
}

// 面板 Teleport 到 body：断言一律走 document 查询而非 wrapper 树。
let router: ReturnType<typeof createAppRouter>

async function mountPalette() {
  vi.stubGlobal('matchMedia', makeMatchMedia())
  const session = createSessionStore(authApi)
  router = createAppRouter(session, createMemoryHistory('/admin/'))
  await router.push('/')
  await router.isReady()
  const wrapper = mount(AppCommandPalette, {
    attachTo: document.body,
    global: {
      plugins: [router],
      provide: { [sessionKey as symbol]: session },
    },
  })
  await flushPromises()
  return wrapper
}

function queryPanel(): Element | null {
  return document.body.querySelector('[data-testid="command-palette"]')
}

function queryInput(): HTMLInputElement {
  const input = document.body.querySelector('input[aria-label="搜索页面或操作"]')
  if (!input) throw new Error('command palette input not found')
  return input as HTMLInputElement
}

describe('AppCommandPalette', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  it('renders nothing while closed and lists navigation commands when opened', async () => {
    await mountPalette()
    expect(queryPanel()).toBeNull()

    const palette = useCommandPalette()
    palette.show()
    await flushPromises()

    const panel = queryPanel()
    expect(panel).not.toBeNull()
    // 导航命令来自路由表：资源接入 + 系统观测两组页面都应出现
    expect(panel?.textContent).toContain('NVIDIA Key')
    expect(panel?.textContent).toContain('系统与观测')
    expect(panel?.textContent).toContain('切换亮 / 暗主题')
    palette.hide()
  })

  it('filters commands by fuzzy query', async () => {
    await mountPalette()
    const palette = useCommandPalette()
    palette.show()
    await flushPromises()

    const input = queryInput()
    expect(input).not.toBeNull()
    input.value = '代理'
    input.dispatchEvent(new Event('input'))
    await flushPromises()

    const panel = queryPanel()?.textContent ?? ''
    expect(panel).toContain('代理池')
    expect(panel).not.toContain('Access Key')
    palette.hide()
  })

  it('shows the empty state when nothing matches', async () => {
    await mountPalette()
    const palette = useCommandPalette()
    palette.show()
    await flushPromises()

    const input = queryInput()
    input.value = '不存在的命令xyz'
    input.dispatchEvent(new Event('input'))
    await flushPromises()

    expect(queryPanel()?.textContent).toContain('没有匹配')
    palette.hide()
  })

  it('runs a command on Enter and closes the palette', async () => {
    await mountPalette()
    const palette = useCommandPalette()
    palette.show()
    await flushPromises()

    // 断言面板契约（发起正确导航请求并关闭自身）而非完整导航结果：
    // happy-dom 下「带查询状态从事件栈发起懒加载导航」会挂起，属环境怪癖；
    // 真实导航集成由 router/index.spec.ts 与 AppShell.spec.ts 覆盖。
    const pushSpy = vi.spyOn(router, 'push').mockResolvedValue(undefined)
    const input = queryInput()
    input.value = '模型白名单'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await flushPromises()

    expect(pushSpy).toHaveBeenCalledWith('/models')
    expect(palette.open.value).toBe(false)
  })

  it('closes on Escape', async () => {
    await mountPalette()
    const palette = useCommandPalette()
    palette.show()
    await flushPromises()

    queryInput()?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(palette.open.value).toBe(false)
  })
})
