<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import { useSession } from '../../features/auth/useSession'
import UiIcon from '../ui/UiIcon.vue'
import UiMenu from '../ui/UiMenu.vue'
import type { IconName } from '../ui'
import AppCommandPalette from './AppCommandPalette.vue'
import ShortcutHelpOverlay from './ShortcutHelpOverlay.vue'
import { useCommandPalette } from '../useCommandPalette'
import { useFocusTrap } from '../useFocusTrap'
import { lockBodyScroll, unlockBodyScroll } from '../useScrollLock'
import { formatCombo, registerHotkey } from '../composables/useHotkeys'
import { toggleTheme, useTheme } from '../useTheme'

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
const palette = useCommandPalette()
const theme = useTheme()
let isDrawerLocked = false

// ── 桌面图标栏模式：折叠后只留图标（68px），偏好持久化。
// 仅影响 lg+ 布局；移动端抽屉始终全宽。 ──
const RAIL_KEY = 'nvr-rail'
const railCollapsed = ref(globalThis.localStorage?.getItem(RAIL_KEY) === '1')

function toggleRail(): void {
  railCollapsed.value = !railCollapsed.value
  if (railCollapsed.value) {
    globalThis.localStorage?.setItem(RAIL_KEY, '1')
  } else {
    globalThis.localStorage?.removeItem(RAIL_KEY)
  }
  void nextTick(syncIndicator)
}

// '/' 全局聚焦搜索：打开命令面板（列表页可注册同名热键覆盖为页内筛选）
registerHotkey({
  id: 'global.focus-search',
  combo: '/',
  description: '打开命令面板',
  group: '通用',
  handler: () => { palette.show() },
})

// 展示用快捷键序列（与命令面板的提示同源；mod 在任何平台 Ctrl 都生效）
const searchKbd = formatCombo('mod+k').join(' ')
const searchHint = `搜索（${searchKbd}）`
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
  if (isDrawerLocked) {
    unlockBodyScroll()
    isDrawerLocked = false
  }
  globalThis.removeEventListener('keydown', onKeydown)
  mediaQuery?.removeEventListener('change', onMediaChange)
  globalThis.removeEventListener('resize', syncIndicator)
})

// Focus cycling is shared with modal surfaces through useFocusTrap. The
// mobile shell still prefers the first navigation link on open and the menu
// button on close, so those entry points remain stable for touch and keyboard users.
useFocusTrap(sidebarOpen, sidebar, () => { sidebarOpen.value = false })
watch(sidebarOpen, async (open) => {
  if (open && isMobile.value) {
    if (!isDrawerLocked) {
      lockBodyScroll()
      isDrawerLocked = true
    }
  } else if (isDrawerLocked) {
    unlockBodyScroll()
    isDrawerLocked = false
  }

  await nextTick()
  if (!isMobile.value) return
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

// ── 侧边栏滑动指示器：一个绝对定位的胶囊跟随激活项平移（FLIP 思想），
// 避免每个链接各自带背景切换时的"跳变"感。测量失败时静默隐藏。 ──
const navListRef = ref<globalThis.HTMLElement | null>(null)
const indicator = ref<{ top: number, height: number, visible: boolean }>({ top: 0, height: 0, visible: false })

function syncIndicator(): void {
  const container = navListRef.value
  if (!container) return
  const active = container.querySelector<globalThis.HTMLElement>('[aria-current="page"]')
  if (!active) {
    indicator.value = { top: 0, height: 0, visible: false }
    return
  }
  indicator.value = {
    top: active.offsetTop,
    height: active.offsetHeight,
    visible: true,
  }
}

watch([() => route.path, navGroups], () => {
  void nextTick(syncIndicator)
}, { immediate: true })

onMounted(() => {
  void nextTick(syncIndicator)
  globalThis.addEventListener('resize', syncIndicator)
})

onBeforeUnmount(() => {
  globalThis.removeEventListener('resize', syncIndicator)
})

function onThemeToggle(event: globalThis.MouseEvent): void {
  toggleTheme({ x: event.clientX, y: event.clientY })
}

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
    <header
      class="fixed inset-x-0 top-0 z-[var(--z-toolbar)] flex h-14 items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-canvas-deep)] px-4 lg:hidden"
    >
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
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-[var(--radius-control)] bg-[var(--color-brand)] text-[11px] font-bold text-[var(--color-brand-foreground)]">
          N
        </div>
        <p class="truncate text-sm font-semibold">
          {{ currentTitle }}
        </p>
      </div>
      <button
        class="icon-btn"
        type="button"
        aria-label="打开命令面板"
        data-testid="open-command-palette-mobile"
        @click="palette.show()"
      >
        <UiIcon
          name="search"
          :size="18"
        />
      </button>
      <button
        class="icon-btn"
        type="button"
        :aria-label="theme.resolvedTheme.value === 'dark' ? '切换到亮色主题' : '切换到暗色主题'"
        data-testid="theme-toggle-mobile"
        @click="onThemeToggle($event)"
      >
        <UiIcon
          :name="theme.resolvedTheme.value === 'dark' ? 'sun' : 'moon'"
          :size="18"
        />
      </button>
    </header>

    <!-- Sidebar overlay (mobile) -->
    <Transition name="fade">
      <button
        v-if="sidebarOpen"
        class="fixed inset-0 z-[var(--z-scrim)] cursor-default bg-[var(--color-overlay)] lg:hidden"
        type="button"
        aria-label="关闭菜单"
        @click="sidebarOpen = false"
      />
    </Transition>

    <!-- Sidebar -->
    <aside
      id="admin-sidebar"
      ref="sidebar"
      class="fixed inset-y-0 left-0 z-[var(--z-drawer)] flex w-60 flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] transition-transform duration-300 lg:translate-x-0"
      :class="[sidebarOpen ? 'translate-x-0' : '-translate-x-full', railCollapsed ? 'lg:w-[68px]' : '']"
      :inert="isMobile && !sidebarOpen"
      aria-label="管理侧栏"
    >
      <div
        data-testid="sidebar-brand"
        class="sidebar-brand flex h-16 items-center gap-3 border-b border-[var(--color-border-subtle)] px-5"
        :class="railCollapsed ? 'lg:justify-center lg:px-0' : ''"
      >
        <div class="flex h-8 w-8 shrink-0 items-center justify-center">
          <span
            data-testid="sidebar-brand-mark"
            class="sidebar-brand-mark flex h-8 w-8 items-center justify-center rounded-[var(--radius-control)] bg-[var(--color-brand)] text-sm font-bold text-[var(--color-brand-foreground)]"
          >
            N
          </span>
        </div>
        <div
          v-if="!railCollapsed"
          class="min-w-0"
        >
          <p class="truncate text-[13px] font-semibold tracking-[0.01em]">
            NVIDIA Router
          </p>
          <p class="mt-0.5 text-[11px] tracking-[0.02em] text-[var(--color-text-muted)]">
            管理控制台
          </p>
        </div>
      </div>

      <!-- 命令面板入口：幽灵极简——透明底+发丝描边，交互时才显形（Warm Restraint） -->
      <div
        class="px-5 pb-3 pt-4"
        :class="railCollapsed ? 'lg:px-3' : ''"
      >
        <button
          class="flex h-10 w-full items-center gap-2.5 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-transparent px-3 text-sm text-[var(--color-text-secondary)] transition-[background-color,border-color,color] duration-[var(--duration-micro)] hover:border-[var(--color-border-strong)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:border-[var(--color-border-strong)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2"
          :class="railCollapsed ? 'lg:justify-center lg:px-0' : ''"
          type="button"
          data-testid="open-command-palette"
          :aria-label="`打开命令面板（${searchKbd}）`"
          :title="searchHint"
          @click="palette.show()"
        >
          <UiIcon
            name="search"
            :size="15"
          />
          <span
            v-if="!railCollapsed"
            class="flex-1 text-left"
          >搜索…</span>
          <kbd
            v-if="!railCollapsed"
            class="kbd"
          >{{ searchKbd }}</kbd>
        </button>
      </div>

      <nav
        class="flex-1 overflow-y-auto px-3 pb-3"
        aria-label="管理功能"
      >
        <div
          ref="navListRef"
          class="relative"
        >
          <!-- 滑动激活指示器：跟随当前项平移，而非逐项切换背景 -->
          <div
            v-if="indicator.visible"
            class="absolute left-0 right-0 top-0 rounded-[var(--radius-control)] bg-[var(--color-active)] transition-[transform,height,opacity] duration-300 ease-[cubic-bezier(0.34,1.3,0.64,1)]"
            :style="{ transform: `translateY(${indicator.top}px)`, height: `${indicator.height}px` }"
            aria-hidden="true"
          />
          <div
            v-for="(group, groupIndex) in navGroups"
            :key="group.label"
          >
            <p
              v-if="!railCollapsed"
              class="nav-group-label"
            >
              {{ group.label }}
            </p>
            <div class="space-y-0.5">
              <RouterLink
                v-for="item in group.items"
                :key="item.path"
                :to="item.path"
                :data-testid="item.testId"
                class="stagger-item relative"
                :class="[isActive(item.path)
                  ? 'nav-link font-medium text-[var(--color-text)]'
                  : 'nav-link', railCollapsed ? 'lg:justify-center lg:px-0' : '']"
                :style="{ '--stagger-index': groupIndex * 3 + group.items.indexOf(item) }"
                :aria-current="isActive(item.path) ? 'page' : undefined"
                :title="item.label"
              >
                <UiIcon
                  :name="item.icon"
                  :size="16"
                  class="relative z-[var(--z-sticky)] shrink-0"
                />
                <span
                  v-if="!railCollapsed"
                  class="relative z-[var(--z-sticky)]"
                >{{ item.label }}</span>
              </RouterLink>
            </div>
          </div>
        </div>
      </nav>

      <!-- 用户区块（Warm Restraint 克制收纳）：身份一行，操作收进 … 菜单；
           折叠态只剩圆形头像触发钮。e2e 依赖 testid：theme-toggle / logout。 -->
      <div
        class="mt-2 border-t border-[var(--color-border-subtle)] p-4"
        :class="railCollapsed ? 'lg:p-2' : ''"
      >
        <!-- 展开态：品牌色头像（角标状态点）+ 身份 + 右侧 … 菜单 -->
        <div
          v-if="!railCollapsed"
          class="flex items-center gap-2.5"
        >
          <div
            class="relative ml-1 shrink-0"
            role="img"
            aria-label="管理员，会话有效"
          >
            <div class="flex h-8 w-8 items-center justify-center rounded-full bg-[var(--color-brand)] text-[13px] font-semibold text-[var(--color-brand-foreground)]">
              管
            </div>
            <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--color-surface)] bg-[var(--color-success)]" />
          </div>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium text-[var(--color-text)]">
              管理员
            </p>
            <p class="sr-only">
              会话有效
            </p>
          </div>
          <UiMenu label="账户操作">
            <template #default="{ close }">
              <button
                class="menu-item"
                role="menuitem"
                type="button"
                data-testid="theme-toggle"
                @click="onThemeToggle($event); close()"
              >
                <UiIcon
                  :name="theme.resolvedTheme.value === 'dark' ? 'sun' : 'moon'"
                  :size="15"
                />
                {{ theme.resolvedTheme.value === 'dark' ? '切换到亮色主题' : '切换到暗色主题' }}
              </button>
              <RouterLink
                class="menu-item"
                role="menuitem"
                to="/change-password"
                @click="close()"
              >
                <UiIcon
                  name="settings"
                  :size="15"
                />
                修改密码
              </RouterLink>
              <button
                class="menu-item text-[var(--color-danger)]"
                role="menuitem"
                type="button"
                data-testid="logout"
                :disabled="loggingOut"
                @click="close(); logout()"
              >
                <UiIcon
                  name="logout"
                  :size="15"
                />
                退出登录
              </button>
            </template>
          </UiMenu>
        </div>

        <!-- 折叠态：仅圆形头像触发同一菜单；面板左对齐溢出 rail 宽度悬浮于内容层 -->
        <div
          v-else
          class="flex justify-center"
        >
          <UiMenu
            label="账户操作"
            trigger-class="h-8 w-8 rounded-full pointer-coarse:h-11 pointer-coarse:w-11"
          >
            <template #trigger>
              <span
                class="relative flex h-8 w-8 items-center justify-center rounded-full bg-[var(--color-brand)] text-[13px] font-semibold text-[var(--color-brand-foreground)]"
                role="img"
                aria-label="管理员，会话有效"
              >
                管
                <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--color-surface)] bg-[var(--color-success)]" />
              </span>
            </template>
            <template #default="{ close }">
              <button
                class="menu-item"
                role="menuitem"
                type="button"
                data-testid="theme-toggle"
                @click="onThemeToggle($event); close()"
              >
                <UiIcon
                  :name="theme.resolvedTheme.value === 'dark' ? 'sun' : 'moon'"
                  :size="15"
                />
                {{ theme.resolvedTheme.value === 'dark' ? '切换到亮色主题' : '切换到暗色主题' }}
              </button>
              <RouterLink
                class="menu-item"
                role="menuitem"
                to="/change-password"
                @click="close()"
              >
                <UiIcon
                  name="settings"
                  :size="15"
                />
                修改密码
              </RouterLink>
              <button
                class="menu-item text-[var(--color-danger)]"
                role="menuitem"
                type="button"
                data-testid="logout"
                :disabled="loggingOut"
                @click="close(); logout()"
              >
                <UiIcon
                  name="logout"
                  :size="15"
                />
                退出登录
              </button>
            </template>
          </UiMenu>
        </div>
      </div>

      <!-- 折叠开关：仅桌面显示 -->
      <button
        class="sidebar-rail-toggle absolute -right-3.5 top-[70px] hidden h-7 w-7 items-center justify-center rounded-full border border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text-muted)] transition-[background-color,border-color,color] duration-[var(--duration-micro)] hover:border-[var(--color-border-strong)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] lg:flex"
        type="button"
        data-testid="toggle-rail"
        :aria-label="railCollapsed ? '展开侧栏' : '折叠侧栏'"
        :title="railCollapsed ? '展开侧栏' : '折叠侧栏'"
        @click="toggleRail"
      >
        <UiIcon
          :name="railCollapsed ? 'panel-left-open' : 'panel-left-close'"
          :size="13"
        />
      </button>
    </aside>

    <main
      class="min-w-0 pt-14 lg:pt-0 transition-[padding] duration-300"
      :class="railCollapsed ? 'lg:pl-[68px]' : 'lg:pl-60'"
    >
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

    <AppCommandPalette />
    <ShortcutHelpOverlay />
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
