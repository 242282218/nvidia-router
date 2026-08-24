<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { formatCombo, registerHotkey, useHotkeyRegistry } from '../composables/useHotkeys'
import { useFocusTrap } from '../useFocusTrap'
import { lockBodyScroll, unlockBodyScroll } from '../useScrollLock'
import UiIcon from '../ui/UiIcon.vue'
import UiKbd from '../ui/UiKbd.vue'

// 快捷键帮助浮层：'?' 唤起，列出注册表全部快捷键（按分组）。
// 自身挂载在 AppShell，随应用生命周期常驻。
defineOptions({ name: 'ShortcutHelpOverlay' })

const open = ref(false)
const panel = ref<globalThis.HTMLElement | null>(null)
let isHelpLocked = false

registerHotkey({
  id: 'help.toggle',
  combo: '?',
  description: '打开/关闭快捷键帮助',
  group: '通用',
  handler: () => { open.value = !open.value },
})

registerHotkey({
  id: 'help.close',
  combo: 'escape',
  description: '关闭浮层',
  group: '通用',
  handler: () => { if (open.value) open.value = false },
})

const registry = useHotkeyRegistry()

const groups = computed(() => {
  const map = new Map<string, { combo: string, description: string }[]>()
  for (const entry of registry.value) {
    if (entry.id.startsWith('help.')) continue
    const list = map.get(entry.group) ?? []
    list.push({ combo: entry.combo, description: entry.description })
    map.set(entry.group, list)
  }
  return [...map.entries()].map(([name, items]) => ({ name, items }))
})

// Esc 关闭由注册表处理器负责；这里兜底路由离开时收起
watch(() => groups.value.length, () => { /* keep reactive */ })

watch(open, (isOpen) => {
  if (isOpen) {
    if (!isHelpLocked) {
      lockBodyScroll()
      isHelpLocked = true
    }
  } else {
    if (isHelpLocked) {
      unlockBodyScroll()
      isHelpLocked = false
    }
  }
})

onBeforeUnmount(() => {
  if (isHelpLocked) {
    unlockBodyScroll()
    isHelpLocked = false
  }
})

function close(): void {
  open.value = false
}

useFocusTrap(open, panel, close)
</script>

<template>
  <Teleport to="body">
    <Transition name="help-fade">
      <div
        v-if="open"
        class="fixed inset-0 z-[var(--z-modal)] flex min-h-dvh items-start justify-center overflow-y-auto bg-[var(--color-overlay)] p-4 sm:items-center"
        @mousedown.self="close"
      >
        <div
          ref="panel"
          class="w-full max-h-[calc(100dvh-2rem)] max-w-lg overflow-y-auto overscroll-contain rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-elevated)] p-6"
          role="dialog"
          aria-modal="true"
          aria-label="键盘快捷键"
        >
          <header class="mb-4 flex items-center justify-between">
            <h2 class="type-heading flex items-center gap-2">
              <UiIcon
                name="keyboard"
                :size="18"
              />
              键盘快捷键
            </h2>
            <button
              type="button"
              class="icon-btn-sm"
              aria-label="关闭帮助"
              @click="close"
            >
              <UiIcon
                name="close"
                :size="15"
              />
            </button>
          </header>

          <div class="space-y-5">
            <section
              v-for="group in groups"
              :key="group.name"
            >
              <h3 class="nav-group-label px-0 pb-2 pt-0">
                {{ group.name }}
              </h3>
              <ul class="space-y-1.5">
                <li
                  v-for="item in group.items"
                  :key="item.combo + item.description"
                  class="flex items-center justify-between gap-4 text-sm"
                >
                  <span class="text-[var(--color-text-secondary)]">{{ item.description }}</span>
                  <span class="flex shrink-0 items-center gap-1">
                    <template
                      v-for="(key, ki) in formatCombo(item.combo)"
                      :key="ki"
                    >
                      <UiKbd>{{ key }}</UiKbd>
                      <span
                        v-if="ki < formatCombo(item.combo).length - 1"
                        class="text-xs text-[var(--color-text-subtle)]"
                      >+</span>
                    </template>
                  </span>
                </li>
              </ul>
            </section>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.help-fade-enter-active {
  transition: opacity var(--duration-local) var(--ease-enter);
}
.help-fade-leave-active {
  transition: opacity var(--duration-micro) var(--ease-exit);
}
.help-fade-enter-from,
.help-fade-leave-to {
  opacity: 0;
}
</style>
