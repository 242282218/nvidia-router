<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import { useSession } from '../../features/auth/useSession'

defineOptions({ name: 'AppShell' })

const router = useRouter()
const route = useRoute()
const session = useSession()
const sidebarOpen = ref(false)
const loggingOut = ref(false)
const menuButton = ref<globalThis.HTMLButtonElement | null>(null)
const sidebar = ref<globalThis.HTMLElement | null>(null)
// The sidebar is a full-time navigation rail on desktop; the mobile drawer is
// only focus-managed (inert when closed, focus moved in/out) below lg.
const isMobile = ref(false)
let mediaQuery: ReturnType<typeof globalThis.matchMedia> | null = null

function onMediaChange(event: globalThis.MediaQueryListEvent): void {
  isMobile.value = event.matches
}

onMounted(() => {
  // happy-dom (unit tests) does not implement matchMedia; guard so the shell
  // still mounts in tests.
  mediaQuery = typeof globalThis.matchMedia === 'function' ? globalThis.matchMedia('(max-width: 1023px)') : null
  isMobile.value = mediaQuery?.matches ?? false
  mediaQuery?.addEventListener('change', onMediaChange)
  globalThis.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  globalThis.removeEventListener('keydown', onKeydown)
  mediaQuery?.removeEventListener('change', onMediaChange)
})

// Focus the drawer when it opens and return focus to the menu button when it
// closes, so keyboard users never lose their place in the document. Runs
// post-flush: the inert attribute must be removed before focus() will land on
// a drawer link (inert elements are unfocusable by spec).
watch(sidebarOpen, async (open) => {
  if (!isMobile.value) return
  await nextTick()
  if (open) {
    sidebar.value?.querySelector<globalThis.HTMLAnchorElement>('nav a')?.focus()
  } else {
    menuButton.value?.focus()
  }
}, { flush: 'post' })

const navItems = [
  { path: '/nvidia-keys', label: 'NVIDIA Key', icon: 'key', testId: 'nav-nvidia-keys' },
  { path: '/providers', label: '提供商', icon: 'provider', testId: 'nav-providers' },
  { path: '/models', label: '模型白名单', icon: 'model', testId: 'nav-models' },
  { path: '/access-keys', label: 'Access Key', icon: 'access', testId: 'nav-access-keys' },
  { path: '/runtime', label: '运行状态', icon: 'runtime', testId: 'nav-runtime' },
  { path: '/statistics', label: '监控', icon: 'stats', testId: 'nav-statistics' },
  { path: '/live', label: '实时', icon: 'live', testId: 'nav-live' },
  { path: '/proxy-pool', label: '代理池', icon: 'proxy', testId: 'nav-proxy-pool' },
  { path: '/audit', label: '审计日志', icon: 'audit', testId: 'nav-audit' },
] as const

const currentLabel = computed(() => navItems.find((item) => item.path === route.path)?.label ?? 'NVIDIA Key')

watch(() => route.path, () => {
  sidebarOpen.value = false
})

function isActive(path: string): boolean {
  return route.path === path || (path === '/nvidia-keys' && route.path === '/')
}

async function logout(): Promise<void> {
  if (loggingOut.value) return
  loggingOut.value = true
  try {
    // useSession.logout always clears local state, even if the remote call
    // fails: the user must leave the authenticated shell for a stale cookie
    // to stop authorising privileged chrome.
    await session.logout()
  } finally {
    loggingOut.value = false
  }
  await router.push('/login')
}

function onKeydown(event: globalThis.KeyboardEvent): void {
  if (event.key === 'Escape') sidebarOpen.value = false
}
</script>

<template>
  <div class="min-h-screen bg-[var(--color-canvas)] text-[var(--color-text)]">
    <!-- Mobile header -->
    <header class="fixed inset-x-0 top-0 z-40 flex h-14 items-center gap-3 border-b border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-canvas)_95%,transparent)] px-4 backdrop-blur-md lg:hidden">
      <button
        ref="menuButton"
        class="btn-ghost h-11 w-11 rounded-lg p-2"
        type="button"
        aria-label="切换菜单"
        :aria-expanded="sidebarOpen"
        aria-controls="admin-sidebar"
        @click="sidebarOpen = !sidebarOpen"
      >
        <svg
          class="h-5 w-5"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            v-if="!sidebarOpen"
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 6h16M4 12h16M4 18h16"
          />
          <path
            v-else
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M6 18L18 6M6 6l12 12"
          />
        </svg>
      </button>
      <div class="min-w-0">
        <p class="truncate text-sm font-semibold">
          NVIDIA API Router
        </p>
        <p class="truncate text-xs text-[var(--color-text-muted)]">
          {{ currentLabel }}
        </p>
      </div>
    </header>

    <!-- Sidebar overlay (mobile) -->
    <Transition name="fade">
      <button
        v-if="sidebarOpen"
        class="fixed inset-0 z-30 cursor-default bg-black/60 backdrop-blur-sm lg:hidden"
        type="button"
        aria-label="关闭菜单"
        @click="sidebarOpen = false"
      />
    </Transition>

    <!-- Sidebar -->
    <aside
      id="admin-sidebar"
      ref="sidebar"
      class="fixed inset-y-0 left-0 z-40 flex w-64 -translate-x-full flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] transition-transform duration-300 lg:translate-x-0"
      :class="sidebarOpen ? 'translate-x-0' : ''"
      :inert="isMobile && !sidebarOpen"
      aria-label="管理侧栏"
    >
      <div class="flex h-16 items-center gap-3 border-b border-[var(--color-border)] px-5">
        <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--color-accent)] text-sm font-bold text-[var(--color-accent-foreground)]">
          N
        </div>
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold">
            NVIDIA Router
          </p>
          <p class="text-xs text-[var(--color-text-muted)]">
            管理控制台
          </p>
        </div>
      </div>

      <div class="border-b border-[var(--color-border)] px-4 py-4">
        <p class="text-xs font-semibold uppercase tracking-[0.12em] text-[var(--color-text-subtle)]">
          运行环境
        </p>
        <div class="mt-2 flex items-center gap-2 text-sm text-[var(--color-text-secondary)]">
          <span
            class="h-2 w-2 rounded-full bg-[var(--color-success)] pulse-dot"
            aria-hidden="true"
          />
          <span>管理端已登录。</span>
        </div>
      </div>

      <nav
        class="flex-1 space-y-1 overflow-y-auto p-3"
        aria-label="管理功能"
      >
        <RouterLink
          v-for="item in navItems"
          :key="item.path"
          :to="item.path"
          :data-testid="item.testId"
          :class="isActive(item.path) ? 'nav-link-active' : 'nav-link'"
          :aria-current="isActive(item.path) ? 'page' : undefined"
        >
          <svg
            v-if="item.icon === 'key'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'model'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M9.75 3.104v5.714a2.25 2.25 0 01-.659 1.591L5 14.5M9.75 3.104c-.251.023-.501.05-.75.082m.75-.082a24.301 24.301 0 014.5 0m0 0v5.714c0 .597.237 1.17.659 1.591L19.8 15.3M14.25 3.104c.251.023.501.05.75.082M19.8 15.3l-1.57.393A9.065 9.065 0 0112 15a9.065 9.065 0 00-6.23.693L5 14.5m14.8.8l1.402 1.402c1.232 1.232.65 3.318-1.067 3.611A48.309 48.309 0 0112 21c-2.773 0-5.491-.235-8.135-.687-1.718-.293-2.3-2.379-1.067-3.61L5 14.5"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'access'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'runtime'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'proxy'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M4.5 7.5h15m-12 4.5h9m-12 4.5h15M6.75 4.5h10.5A2.25 2.25 0 0119.5 6.75v10.5a2.25 2.25 0 01-2.25 2.25H6.75a2.25 2.25 0 01-2.25-2.25V6.75A2.25 2.25 0 016.75 4.5z"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'live'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z"
            />
          </svg>
          <svg
            v-else-if="item.icon === 'audit'"
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z"
            />
          </svg>
          <svg
            v-else
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125 0 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z"
            />
          </svg>
          {{ item.label }}
        </RouterLink>
      </nav>

      <div class="border-t border-[var(--color-border)] p-3">
        <button
          data-testid="logout"
          class="nav-link w-full"
          type="button"
          :disabled="loggingOut"
          @click="logout"
        >
          <svg
            class="h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="1.5"
              d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9"
            />
          </svg>
          {{ loggingOut ? '退出中…' : '退出登录' }}
        </button>
      </div>
    </aside>

    <main class="min-w-0 lg:pl-64 lg:pt-0 pt-14">
      <div class="mx-auto flex min-h-14 max-w-[1440px] items-center justify-between border-b border-[var(--color-border)] px-4 sm:px-6 lg:px-8">
        <div class="hidden items-center gap-2 text-sm text-[var(--color-text-muted)] lg:flex">
          <span
            class="h-1.5 w-1.5 rounded-full bg-[var(--color-success)]"
            aria-hidden="true"
          />
          管理端已登录。
        </div>
        <p class="ml-auto hidden text-xs font-mono text-[var(--color-text-subtle)] lg:block">
          {{ route.path }}
        </p>
      </div>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.fade-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.fade-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
