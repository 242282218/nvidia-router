<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, type Router } from 'vue-router'

import UiIcon from '../ui/UiIcon.vue'
import type { IconName } from '../ui'
import { toggleTheme } from '../useTheme'
import { useCommandPalette } from '../useCommandPalette'
import { useSession } from '../../features/auth/useSession'

defineOptions({ name: 'AppCommandPalette' })

interface CommandItem {
  id: string
  label: string
  group: string
  icon: IconName
  keywords?: string
  run: () => void | Promise<void>
}

const router = useRouter()
const session = useSession()
const { open, hide } = useCommandPalette()

const query = ref('')
const activeIndex = ref(0)
const inputRef = ref<globalThis.HTMLInputElement | null>(null)
const listRef = ref<globalThis.HTMLElement | null>(null)

// 命令来源与侧边栏同源：路由 meta.nav 是导航命令的唯一事实来源。
function navCommands(routerInstance: Router): CommandItem[] {
  const root = routerInstance.options.routes.find((record) => record.path === '/')
  const items: CommandItem[] = []
  for (const child of root?.children ?? []) {
    const nav = child.meta?.nav
    const title = child.meta?.title
    if (!nav || !title) continue
    items.push({
      id: `nav:${child.path}`,
      label: title,
      group: '页面导航',
      icon: nav.icon,
      run: () => void routerInstance.push(`/${child.path}`),
    })
  }
  return items.sort((a, b) => a.id.localeCompare(b.id))
}

const staticCommands = computed<CommandItem[]>(() => [
  {
    id: 'action:theme',
    label: '切换亮 / 暗主题',
    group: '快捷操作',
    icon: 'moon',
    keywords: 'dark light theme 主题 暗色 深色 亮色',
    run: () => toggleTheme(),
  },
  {
    id: 'action:change-password',
    label: '修改管理员密码',
    group: '快捷操作',
    icon: 'settings',
    keywords: 'password 密码 账号',
    run: () => void router.push('/change-password'),
  },
  {
    id: 'action:logout',
    label: '退出登录',
    group: '快捷操作',
    icon: 'logout',
    keywords: 'logout 登出 会话',
    run: async () => {
      await session.logout()
      await router.push('/login')
    },
  },
])

// 子序列模糊匹配：query 字符按序出现在候选文本中即命中；
// 首字符命中越早、整体跨度越短排名越高。
function fuzzyScore(text: string, q: string): number {
  if (!q) return 1
  const lower = text.toLowerCase()
  const needle = q.toLowerCase()
  let cursor = 0
  let score = 0
  for (const char of needle) {
    const found = lower.indexOf(char, cursor)
    if (found === -1) return 0
    score += found === cursor ? 0 : 1
    if (found === 0) score -= 2
    cursor = found + 1
  }
  return 100 - score - (lower.length - needle.length) * 0.05
}

const filteredGroups = computed<Array<{ group: string, items: CommandItem[] }>>(() => {
  const all = [...navCommands(router), ...staticCommands.value]
  const matched = query.value.trim()
    ? all
      .map((item) => ({ item, score: fuzzyScore(`${item.label} ${item.keywords ?? ''}`, query.value.trim()) }))
      .filter((entry) => entry.score > 0)
      .sort((a, b) => b.score - a.score)
      .map((entry) => entry.item)
    : all
  const groups = new Map<string, CommandItem[]>()
  for (const item of matched) {
    const list = groups.get(item.group) ?? []
    list.push(item)
    groups.set(item.group, list)
  }
  return [...groups.entries()].map(([group, items]) => ({ group, items }))
})

const flatResults = computed(() => filteredGroups.value.flatMap((group) => group.items))

watch(flatResults, (results) => {
  if (activeIndex.value >= results.length) activeIndex.value = Math.max(0, results.length - 1)
})

watch(open, async (isOpen) => {
  if (!isOpen) return
  query.value = ''
  activeIndex.value = 0
  await nextTick()
  inputRef.value?.focus()
})

watch(query, () => {
  activeIndex.value = 0
})

async function runCommand(item: CommandItem): Promise<void> {
  hide()
  await item.run()
}

function onKeydown(event: globalThis.KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    hide()
    return
  }
  const total = flatResults.value.length
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (total > 0) activeIndex.value = (activeIndex.value + 1) % total
    scrollActiveIntoView()
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (total > 0) activeIndex.value = (activeIndex.value - 1 + total) % total
    scrollActiveIntoView()
  } else if (event.key === 'Enter') {
    event.preventDefault()
    const item = flatResults.value[activeIndex.value]
    if (item) void runCommand(item)
  }
}

function scrollActiveIntoView(): void {
  void nextTick(() => {
    listRef.value
      ?.querySelector('[data-active="true"]')
      ?.scrollIntoView({ block: 'nearest' })
  })
}

let globalKeyHandler: ((event: globalThis.KeyboardEvent) => void) | null = null

onMounted(() => {
  globalKeyHandler = (event: globalThis.KeyboardEvent) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault()
      open.value = !open.value
    }
  }
  globalThis.addEventListener('keydown', globalKeyHandler)
})

onBeforeUnmount(() => {
  if (globalKeyHandler) globalThis.removeEventListener('keydown', globalKeyHandler)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="palette">
      <div
        v-if="open"
        class="fixed inset-0 z-[60] flex items-start justify-center bg-[var(--color-overlay)] p-4 pt-[12vh] backdrop-blur-sm"
        role="dialog"
        aria-modal="true"
        aria-label="命令面板"
        @click.self="hide"
      >
        <div
          class="w-full max-w-xl overflow-hidden rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-elevated)] shadow-[var(--shadow-overlay)] animate-scale-in"
          data-testid="command-palette"
        >
          <div class="flex items-center gap-3 border-b border-[var(--color-border)] px-4">
            <UiIcon
              name="search"
              :size="16"
              class="shrink-0 text-[var(--color-text-subtle)]"
            />
            <input
              ref="inputRef"
              v-model="query"
              class="h-13 w-full bg-transparent py-4 text-sm text-[var(--color-text)] placeholder:text-[var(--color-text-subtle)] focus:outline-none"
              type="text"
              placeholder="搜索页面或操作…"
              aria-label="搜索页面或操作"
              role="combobox"
              aria-expanded="true"
              :aria-controls="'command-palette-listbox'"
              :aria-activedescendant="flatResults[activeIndex] ? `command-option-${activeIndex}` : undefined"
              @keydown="onKeydown"
            >
            <kbd class="hidden shrink-0 rounded border border-[var(--color-border)] bg-[var(--color-sunken)] px-1.5 py-0.5 text-[11px] text-[var(--color-text-subtle)] sm:block">esc</kbd>
          </div>

          <div
            ref="listRef"
            class="max-h-[46vh] overflow-y-auto p-2"
          >
            <template v-if="flatResults.length > 0">
              <div
                v-for="group in filteredGroups"
                :key="group.group"
                role="group"
                :aria-label="group.group"
              >
                <p class="px-3 pb-1 pt-3 text-[11px] font-medium uppercase tracking-[0.1em] text-[var(--color-text-subtle)] first:pt-1">
                  {{ group.group }}
                </p>
                <button
                  v-for="item in group.items"
                  :id="`command-option-${flatResults.indexOf(item)}`"
                  :key="item.id"
                  class="flex h-10 w-full items-center gap-3 rounded-[var(--radius-control)] px-3 text-left text-sm transition-colors duration-[var(--duration-micro)]"
                  :class="flatResults[activeIndex]?.id === item.id
                    ? 'bg-[var(--color-active)] text-[var(--color-text)]'
                    : 'text-[var(--color-text-secondary)]'"
                  :data-active="flatResults[activeIndex]?.id === item.id ? 'true' : undefined"
                  type="button"
                  role="option"
                  :aria-selected="flatResults[activeIndex]?.id === item.id"
                  @mouseenter="activeIndex = flatResults.indexOf(item)"
                  @click="runCommand(item)"
                >
                  <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-[var(--color-border-subtle)] bg-[var(--color-sunken)] text-[var(--color-text-muted)]">
                    <UiIcon
                      :name="item.icon"
                      :size="13"
                    />
                  </span>
                  <span class="min-w-0 flex-1 truncate">{{ item.label }}</span>
                  <UiIcon
                    v-if="flatResults[activeIndex]?.id === item.id"
                    name="arrow-right"
                    :size="14"
                    class="shrink-0 text-[var(--color-text-subtle)]"
                  />
                </button>
              </div>
            </template>
            <div
              v-else
              class="flex flex-col items-center gap-2 px-4 py-10 text-center"
            >
              <UiIcon
                name="inbox"
                :size="24"
                class="text-[var(--color-text-subtle)]"
              />
              <p class="text-sm text-[var(--color-text-muted)]">
                没有匹配「{{ query }}」的结果
              </p>
            </div>
          </div>

          <div class="flex items-center gap-4 border-t border-[var(--color-border)] px-4 py-2.5 text-[11px] text-[var(--color-text-subtle)]">
            <span class="flex items-center gap-1"><kbd class="rounded border border-[var(--color-border)] bg-[var(--color-sunken)] px-1">↑↓</kbd> 选择</span>
            <span class="flex items-center gap-1"><kbd class="rounded border border-[var(--color-border)] bg-[var(--color-sunken)] px-1">↵</kbd> 打开</span>
            <span class="ml-auto hidden items-center gap-1 sm:flex"><kbd class="rounded border border-[var(--color-border)] bg-[var(--color-sunken)] px-1">⌘K</kbd> 唤起</span>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.palette-enter-active {
  transition: opacity var(--duration-local) cubic-bezier(0, 0, 0.2, 1);
}

.palette-leave-active {
  transition: opacity var(--duration-micro) cubic-bezier(0.4, 0, 1, 1);
}

.palette-enter-from,
.palette-leave-to {
  opacity: 0;
}
</style>
