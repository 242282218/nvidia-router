import { beforeEach, describe, expect, it, vi } from 'vitest'

import type * as themeModule from './useTheme'

// useTheme 是模块级单例（跨组件共享主题状态）；每个用例重置模块，
// 保证 preference/resolved 从干净的初始值开始。
let theme: typeof themeModule

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

async function loadTheme(systemDark: boolean): Promise<void> {
  vi.resetModules()
  vi.stubGlobal('matchMedia', makeMatchMedia(systemDark))
  theme = await import('./useTheme')
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    delete document.documentElement.dataset.theme
  })

  it('defaults to system preference and resolves light when system is light', async () => {
    await loadTheme(false)
    theme.initTheme()
    const { resolvedTheme, preference } = theme.useTheme()
    expect(preference.value).toBe('system')
    expect(resolvedTheme.value).toBe('light')
    expect(document.documentElement.dataset.theme).toBeUndefined()
  })

  it('applies the dark attribute when switching to dark', async () => {
    await loadTheme(false)
    theme.initTheme()
    theme.setThemeMode('dark')
    expect(theme.useTheme().resolvedTheme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('persists an explicit preference to localStorage', async () => {
    await loadTheme(false)
    theme.initTheme()
    theme.setThemeMode('dark')
    expect(localStorage.getItem('nvr-theme')).toBe('dark')

    // 模拟重新加载：新模块实例从存储恢复 dark。
    await loadTheme(false)
    expect(theme.useTheme().preference.value).toBe('dark')
    theme.initTheme()
    expect(theme.useTheme().resolvedTheme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('toggles between themes', async () => {
    await loadTheme(false)
    theme.initTheme()
    const { resolvedTheme } = theme.useTheme()
    const before = resolvedTheme.value
    theme.toggleTheme()
    expect(resolvedTheme.value).toBe(before === 'dark' ? 'light' : 'dark')
  })

  it('follows the system dark preference when preference is system', async () => {
    await loadTheme(true)
    theme.initTheme()
    expect(theme.useTheme().resolvedTheme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })
})
