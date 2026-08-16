<script setup lang="ts">
import LoadingSpinner from './LoadingSpinner.vue'

// Unified loading / error / empty panel for page-level async data. The
// pre-refactor codebase expressed these three states in three different
// shapes per view; the data-table contract requires the error state to be
// recoverable (retry) and the empty state to explain itself.
defineOptions({ name: 'StatePanel' })

withDefaults(defineProps<{
  loading?: boolean
  error?: string
  empty?: boolean
  loadingLabel?: string
  emptyLabel?: string
  /** Optional second line guiding the user to the first record. */
  emptyHint?: string
  retryLabel?: string
  /** Test ids differ per page; e2e locates the error block through them. */
  errorTestId?: string
  retryTestId?: string
}>(), {
  loading: false,
  error: '',
  empty: false,
  loadingLabel: '加载中…',
  emptyLabel: '暂无数据。',
  emptyHint: '',
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
      class="card p-6"
    >
      <LoadingSpinner :label="loadingLabel" />
    </div>
    <div
      v-else-if="error"
      :data-testid="errorTestId || undefined"
      class="card flex flex-wrap items-center justify-between gap-3 p-6 text-sm text-[var(--color-danger)]"
      role="alert"
    >
      <span>{{ error }}</span>
      <button
        :data-testid="retryTestId || undefined"
        class="btn-secondary"
        type="button"
        @click="$emit('retry')"
      >
        {{ retryLabel }}
      </button>
    </div>
    <div
      v-else-if="empty"
      class="card px-6 py-10 text-center"
    >
      <p class="text-sm text-[var(--color-text-secondary)]">
        {{ emptyLabel }}
      </p>
      <p
        v-if="emptyHint"
        class="mt-1.5 text-xs text-[var(--color-text-muted)]"
      >
        {{ emptyHint }}
      </p>
    </div>
    <slot v-else />
  </div>
</template>
