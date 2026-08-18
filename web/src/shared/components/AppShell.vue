<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import { useSession } from '../../features/auth/useSession'
import UiIcon from '../ui/UiIcon.vue'
import type { IconName } from '../ui'

defineOptions({ name: 'AppShell' })

interface NavItem {
  path: string
  label: string
  icon: IconName
  testId: string
}

interface NavGroup {
  label: string
  items: NavItem[]
}

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

// 导航由路由 meta 派生：分组、图标、排序的唯一事实来源在路由表里。
const navGroups = computed<NavGroup[]>(() => {
  const root = router.options.routes.find((record) => record.path === '/')
  const items: (NavItem & { group: string; order: number })[] = []
  for (const child of root?.children ?? []) {
    const nav = child.meta?.nav
    const title = child.meta?.title
    if (!nav || !title) continue
    items.push({
      path: `/${child.path}`,
      label: title,
      icon: nav.icon,
      testId: nav.testId,
      group: nav.group,
      order: nav.order,
    })
  }
  items.sort((a, b) => a.order - b.order)
  const groups = new Map<string, NavItem[]>()
  for (const item of items) {
    const list = groups.get(item.group) ?? []
    list.push({ path: item.path, label: item.label, icon: item.icon, testId: item.testId })
    groups.set(item.group, list)
  }
  return [...groups.entries()].map(([label, groupItems]) => ({ label, items: groupItems }))
})

const currentTitle = computed(() => route.meta.title ?? 'NVIDIA Key')

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
  <div class="min-h-screen text-[var(--color-text)]">
    <!-- Mobile header -->
    <header class="fixed inset-x-0 top-0 z-40 flex h-14 items-center gap-3 border-b border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-canvas)_92%,transparent)] px-4 backdrop-blur-md lg:hidden">
      <button
        ref="menuButton"
        class="icon-btn"
        type="button"
        aria-label="切换菜单"
        :aria-expanded="sidebarOpen"
        aria-controls="admin-sidebar"
        @click="sidebarOpen = !sidebarOpen"
      >
        <UiIcon
          :name="sidebarOpen ? 'close' : 'menu'"
          :size="20"
        />
      </button>
      <div class="flex min-w-0 items-center gap-2.5">
        <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-[var(--color-brand)] text-[11px] font-bold text-[var(--color-brand-foreground)]">
          N
        </div>
        <p class="truncate text-sm font-semibold tracking-[-0.01em]">
          {{ currentTitle }}
        </p>
      </div>
    </header>

    <!-- Sidebar overlay (mobile) -->
    <Transition name="fade">
      <button
        v-if="sidebarOpen"
        class="fixed inset-0 z-30 cursor-default bg-[var(--color-overlay)] backdrop-blur-sm lg:hidden"
        type="button"
        aria-label="关闭菜单"
        @click="sidebarOpen = false"
      />
    </Transition>

    <!-- Sidebar -->
    <aside
      id="admin-sidebar"
      ref="sidebar"
      class="fixed inset-y-0 left-0 z-40 flex w-60 -translate-x-full flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] transition-transform duration-300 lg:translate-x-0"
      :class="sidebarOpen ? 'translate-x-0' : ''"
      :inert="isMobile && !sidebarOpen"
      aria-label="管理侧栏"
    >
      <div class="flex h-16 items-center gap-3 px-5">
        <div class="flex h-8 w-8 items-center justify-center rounded-[10px] bg-[var(--color-brand)] text-sm font-bold text-[var(--color-brand-foreground)] shadow-[var(--shadow-xs)]">
          N
        </div>
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold tracking-[-0.01em]">
            NVIDIA Router
          </p>
          <p class="text-xs text-[var(--color-text-muted)]">
            管理控制台
          </p>
        </div>
      </div>

      <nav
        class="flex-1 overflow-y-auto px-3 pb-3"
        aria-label="管理功能"
      >
        <div
          v-for="group in navGroups"
          :key="group.label"
        >
          <p class="nav-group-label">
            {{ group.label }}
          </p>
          <div class="space-y-0.5">
            <RouterLink
              v-for="item in group.items"
              :key="item.path"
              :to="item.path"
              :data-testid="item.testId"
              :class="isActive(item.path) ? 'nav-link-active' : 'nav-link'"
              :aria-current="isActive(item.path) ? 'page' : undefined"
            >
              <UiIcon
                :name="item.icon"
                :size="16"
                class="shrink-0"
              />
              {{ item.label }}
            </RouterLink>
          </div>
        </div>
      </nav>

      <!-- 用户区块：会话状态 + 账户操作（修改密码此前无入口，只能手输地址） -->
      <div class="border-t border-[var(--color-border)] p-3">
        <div class="flex items-center gap-2.5 rounded-[var(--radius-control)] px-2 py-1.5">
          <div
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--color-sunken)] text-[var(--color-text-muted)]"
            aria-hidden="true"
          >
            <UiIcon
              name="user"
              :size="16"
            />
          </div>
          <div class="min-w-0 flex-1">
            <p class="truncate text-[13px] font-medium text-[var(--color-text-secondary)]">
              管理员
            </p>
            <p class="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
              <span
                class="h-1.5 w-1.5 rounded-full bg-[var(--color-success)] pulse-dot"
                aria-hidden="true"
              />
              会话有效
            </p>
          </div>
          <RouterLink
            class="icon-btn h-8 w-8"
            to="/change-password"
            aria-label="修改管理员密码"
            title="修改密码"
          >
            <UiIcon
              name="settings"
              :size="15"
            />
          </RouterLink>
          <button
            data-testid="logout"
            class="icon-btn h-8 w-8"
            type="button"
            :disabled="loggingOut"
            aria-label="退出登录"
            title="退出登录"
            @click="logout"
          >
            <UiIcon
              name="logout"
              :size="15"
            />
          </button>
        </div>
      </div>
    </aside>

    <main class="min-w-0 pt-14 lg:pl-60 lg:pt-0">
      <RouterView v-slot="{ Component }">
        <Transition
          name="page"
          mode="out-in"
        >
          <component
            :is="Component"
            :key="route.path"
          />
        </Transition>
      </RouterView>
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
