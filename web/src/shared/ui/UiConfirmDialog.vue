<script setup lang="ts">
import UiButton from './UiButton.vue'
import UiIcon from './UiIcon.vue'
import UiModal from './UiModal.vue'

// 破坏性操作确认框：替代原先「点两次按钮」的两步确认。
// 操作路径：点击删除 → 明确的后果说明 → 确认/取消，意图表达完整、
// 不会误触第二次点击，也便于键盘操作（Esc 取消）。
defineOptions({ name: 'UiConfirmDialog' })

withDefaults(defineProps<{
  open: boolean
  title: string
  /** 后果说明，直接告诉用户会发生什么。 */
  message: string
  confirmLabel?: string
  cancelLabel?: string
  tone?: 'danger' | 'primary'
  busy?: boolean
  /** 测试锚点：确认/取消按钮。 */
  confirmTestId?: string
  cancelTestId?: string
}>(), {
  confirmLabel: '确认',
  cancelLabel: '取消',
  tone: 'danger',
  busy: false,
  confirmTestId: undefined,
  cancelTestId: undefined,
})

const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>

<template>
  <UiModal
    :open="open"
    :title="title"
    size="sm"
    @close="emit('cancel')"
  >
    <div class="flex items-start gap-3">
      <div
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full"
        :class="tone === 'danger' ? 'bg-[var(--color-danger-background)] text-[var(--color-danger-foreground)]' : 'bg-[var(--color-info-background)] text-[var(--color-info-foreground)]'"
        aria-hidden="true"
      >
        <UiIcon
          :name="tone === 'danger' ? 'warning' : 'info-circle'"
          :size="18"
        />
      </div>
      <p class="pt-1.5 text-sm leading-relaxed text-[var(--color-text-secondary)]">
        {{ message }}
      </p>
    </div>
    <template #footer>
      <UiButton
        variant="ghost"
        :disabled="busy"
        :data-testid="cancelTestId"
        @click="emit('cancel')"
      >
        {{ cancelLabel }}
      </UiButton>
      <UiButton
        :variant="tone === 'danger' ? 'danger' : 'primary'"
        :loading="busy"
        loading-label="处理中…"
        :data-testid="confirmTestId"
        @click="emit('confirm')"
      >
        {{ confirmLabel }}
      </UiButton>
    </template>
  </UiModal>
</template>
