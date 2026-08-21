import { computed, ref, watch } from 'vue'

// 主题偏好：light/dark 显式指定，system 跟随系统并实时响应系统切换。
export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'nvr-theme'

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

function loadPreference(): ThemePreference {
  try {
    const stored = globalThis.localStorage?.getItem(STORAGE_KEY)
    if (isThemePreference(stored)) return stored
  } catch {
    // localStorage may throw in restricted contexts; fall through to system.
  }
  return 'system'
}

const preference = ref<ThemePreference>(loadPreference())

function systemPrefersDark(): boolean {
  return typeof globalThis.matchMedia === 'function'
    && globalThis.matchMedia('(prefers-color-scheme: dark)').matches
}

const resolved = ref<ResolvedTheme>(
  preference.value === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : preference.value,
)

function applyTheme(theme: ResolvedTheme): void {
  resolved.value = theme
  const root = globalThis.document?.documentElement
  if (!root) return
  if (theme === 'dark') root.dataset.theme = 'dark'
  else delete root.dataset.theme
}

// 主题切换是低频用户动作：同步 flush 让 DOM 属性立即落地，
// 避免异步队列期间出现旧主题闪帧。
watch(resolved, (theme) => {
  const root = globalThis.document?.documentElement
  if (!root) return
  if (theme === 'dark') root.dataset.theme = 'dark'
  else delete root.dataset.theme
}, { flush: 'sync' })

watch(preference, (next) => {
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, next)
  } catch {
    // Persistence is best-effort; the in-memory preference still applies.
  }
  if (next !== 'system') applyTheme(next)
  else applyTheme(systemPrefersDark() ? 'dark' : 'light')
}, { flush: 'sync' })

let systemQuery: MediaQueryList | null = null
let systemListener: ((event: MediaQueryListEvent) => void) | null = null

/** 应用启动时调用一次：落 DOM 属性并订阅系统主题变化。可安全重复调用。 */
export function initTheme(): void {
  applyTheme(
    preference.value === 'system'
      ? (systemPrefersDark() ? 'dark' : 'light')
      : preference.value,
  )
  if (systemQuery || typeof globalThis.matchMedia !== 'function') return
  systemQuery = globalThis.matchMedia('(prefers-color-scheme: dark)')
  systemListener = (event) => {
    if (preference.value === 'system') applyTheme(event.matches ? 'dark' : 'light')
  }
  systemQuery.addEventListener('change', systemListener)
}

/**
 * 切换亮/暗。传入触发事件时用 View Transitions API 做圆形扩散：
 * 新主题快照以点击点为圆心展开，旧主题静止垫底。
 * 不支持该 API 或用户偏好减少动效时直接切换，不产生任何动画。
 */
export function setThemeMode(next: Exclude<ThemePreference, 'system'>, origin?: { x: number, y: number }): void {
  const doc = globalThis.document as (Document & {
    startViewTransition?: (update: () => void) => { ready: Promise<void> }
  }) | undefined
  const reduceMotion = typeof globalThis.matchMedia === 'function'
    && globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches

  if (!doc?.startViewTransition || reduceMotion || !origin) {
    preference.value = next
    return
  }

  const transition = doc.startViewTransition(() => {
    preference.value = next
  })
  void transition.ready.then(() => {
    const root = doc.documentElement
    const radius = Math.hypot(
      Math.max(origin.x, root.clientWidth - origin.x),
      Math.max(origin.y, root.clientHeight - origin.y),
    )
    // WAAPI on the pseudo-element keeps the snapshot animation declarative;
    // failures here must never break the theme switch itself.
    root.animate(
      {
        clipPath: [
          `circle(0px at ${origin.x}px ${origin.y}px)`,
          `circle(${radius}px at ${origin.x}px ${origin.y}px)`,
        ],
      },
      {
        duration: 450,
        easing: 'cubic-bezier(0.4, 0, 0.2, 1)',
        pseudoElement: '::view-transition-new(root)',
      },
    )
  }).catch(() => {})
}

export function toggleTheme(origin?: { x: number, y: number }): void {
  setThemeMode(resolved.value === 'dark' ? 'light' : 'dark', origin)
}

export function useTheme() {
  return {
    preference,
    resolvedTheme: computed(() => resolved.value),
    setThemeMode,
    toggleTheme,
  }
}
