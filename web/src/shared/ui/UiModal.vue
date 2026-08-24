<script setup lang="ts">
import { ref, toRef } from 'vue'

import { useDialog } from '../useDialog'
import UiIcon from './UiIcon.vue'

// 模态框原语：焦点圈禁、Esc 关闭、背景滚动锁定、焦点归还全部由
// useDialog 保证；这里负责统一的视觉骨架（overlay / panel / 标题区 / 底部操作区）。
defineOptions({ name: 'UiModal' })

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  subtitle?: string
  /** 面板宽度档：sm 确认框 / md 表单 / lg 宽表。 */
  size?: 'sm' | 'md' | 'lg'
}>(), { subtitle: undefined, size: 'md' })

const emit = defineEmits<{ close: [] }>()

const panel = ref<globalThis.HTMLElement | null>(null)
useDialog(toRef(props, 'open'), panel, () => emit('close'))

const widthClass: Record<'sm' | 'md' | 'lg', string> = {
  sm: 'max-w-md',
  md: 'max-w-2xl',
  lg: 'max-w-4xl',
}
</script>

<template>
  <!-- 内联渲染而非 Teleport：视图根没有 overflow/transform 祖先，fixed overlay
       表现一致；同时保持测试与 e2e 的 DOM 查询路径稳定（查询宿主内即可完成）。 -->
  <Transition name="modal">
    <div
      v-if="open"
      class="modal-overlay"
      @mousedown.self="emit('close')"
    >
      <div
        ref="panel"
        class="modal-panel"
        :class="widthClass[size]"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        tabindex="-1"
      >
        <header class="flex items-start justify-between gap-4 border-b border-[var(--color-border)] px-6 py-4">
          <div class="min-w-0">
            <h2 class="type-heading">
              {{ title }}
            </h2>
            <p
              v-if="subtitle"
              class="mt-0.5 text-xs text-[var(--color-text-muted)]"
            >
              {{ subtitle }}
            </p>
          </div>
          <button
            class="icon-btn -mr-2 -mt-1"
            type="button"
            aria-label="关闭对话框"
            @click="emit('close')"
          >
            <UiIcon
              name="close"
              :size="18"
            />
          </button>
        </header>

        <div class="max-h-[min(70vh,calc(100dvh-9rem))] overflow-y-auto overscroll-contain px-6 py-5">
          <slot />
        </div>

        <footer
          v-if="$slots.footer"
          class="flex flex-wrap items-center justify-end gap-2 border-t border-[var(--color-border)] px-6 py-4"
        >
          <slot name="footer" />
        </footer>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.modal-enter-active {
  transition: opacity var(--duration-overlay) var(--ease-enter);
}
.modal-leave-active {
  transition: opacity 0.18s var(--ease-exit);
}
.modal-enter-active .modal-panel {
  transition: opacity var(--duration-overlay) var(--ease-enter), transform var(--duration-overlay) var(--ease-enter);
}
.modal-leave-active .modal-panel {
  transition: opacity 0.18s var(--ease-exit), transform 0.18s var(--ease-exit);
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from .modal-panel {
  opacity: 0;
  transform: translateY(12px) scale(0.98);
}
.modal-leave-to .modal-panel {
  opacity: 0;
  transform: translateY(6px) scale(0.99);
}
</style>
