<script setup lang="ts">
import UiButton from './UiButton.vue'
import UiEmptyState from './UiEmptyState.vue'
import UiSkeleton from './UiSkeleton.vue'
import UiIcon from './UiIcon.vue'
import type { IconName } from './icons'

// 异步四态面板：loading（骨架屏）/ error（可恢复）/ empty（可引导）/ 内容。
// 每个列表页共享同一契约，视图只需声明当前状态。
defineOptions({ name: 'UiStatePanel' })

withDefaults(defineProps<{
  loading?: boolean
  error?: string
  empty?: boolean
  loadingLabel?: string
  /** 骨架轮廓：贴近最终内容的形状，避免布局跳变。 */
  skeleton?: 'text' | 'table' | 'cards'
  skeletonLines?: number
  emptyLabel?: string
  /** 空状态第二行：告诉用户第一条数据从哪来。 */
  emptyHint?: string
  emptyIcon?: IconName
  retryLabel?: string
  /** Test ids differ per page; e2e locates the error block through them. */
  errorTestId?: string
  retryTestId?: string
}>(), {
  loading: false,
  error: '',
  empty: false,
  loadingLabel: '加载中…',
  skeleton: 'table',
  skeletonLines: 4,
  emptyLabel: '暂无数据。',
  emptyHint: '',
  emptyIcon: 'inbox',
  retryLabel: '重新加载',
  errorTestId: undefined,
  retryTestId: undefined,
})

defineEmits<{ retry: [] }>()
</script>

<template>
  <div>
    <div
      v-if="loading"
      role="status"
      :aria-label="loadingLabel"
    >
      <UiSkeleton
        :variant="skeleton"
        :lines="skeletonLines"
      />
      <span class="sr-only">{{ loadingLabel }}</span>
    </div>

    <div
      v-else-if="error"
      :data-testid="errorTestId || undefined"
      class="card flex flex-wrap items-center justify-between gap-3 border-[var(--color-danger-background)] bg-[var(--color-danger-background)] p-5"
      role="alert"
    >
      <div class="flex min-w-0 items-start gap-2.5 text-sm text-[var(--color-danger)]">
        <UiIcon
          name="warning"
          :size="16"
          class="mt-0.5 shrink-0"
        />
        <span>{{ error }}</span>
      </div>
      <UiButton
        :data-testid="retryTestId || undefined"
        variant="secondary"
        size="sm"
        icon="refresh"
        @click="$emit('retry')"
      >
        {{ retryLabel }}
      </UiButton>
    </div>

    <div
      v-else-if="empty"
      class="card"
    >
      <UiEmptyState
        :icon="emptyIcon"
        :title="emptyLabel"
        :hint="emptyHint"
      />
    </div>

    <slot v-else />
  </div>
</template>
