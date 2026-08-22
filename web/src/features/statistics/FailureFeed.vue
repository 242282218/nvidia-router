<script setup lang="ts">
import { computed } from 'vue'

import UiButton from '../../shared/ui/UiButton.vue'
import { formatAverageLatency, formatLogDate } from './format'
import type { RequestLog } from './types'

// 最近失败请求 Feed（设计 §5.4）：两行条目 + 1px 分隔线。点击条目把
// model_id + outcome=failure 带回筛选面板，形成"看到问题 → 定位请求"闭环。
defineOptions({ name: 'FailureFeed' })

const props = defineProps<{
  logs: RequestLog[]
  error?: string
  loading?: boolean
}>()

const emit = defineEmits<{ retry: []; select: [modelId: string] }>()

interface FeedEntry {
  log: RequestLog
  time: string
}

const entries = computed<FeedEntry[]>(() => props.logs.map((log) => ({
  log,
  time: formatLogDate(log.created_at),
})))

function selectFailure(log: RequestLog): void {
  // 无模型的失败请求（如 404 unknown model）只能带 outcome 维度。
  if (log.model_id) emit('select', log.model_id)
}
</script>

<template>
  <section
    class="card flex flex-col overflow-hidden"
    aria-label="最近失败请求"
    data-testid="failure-feed"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <div>
        <h3 class="type-heading">
          最近失败请求
        </h3>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          当前窗口内最新失败；点击按模型过滤明细。
        </p>
      </div>
      <span
        v-if="entries.length"
        class="badge-danger"
      >{{ entries.length }} 条</span>
    </div>

    <p
      v-if="error"
      class="m-4 flex flex-wrap items-center gap-3 text-sm text-[var(--color-danger)]"
      role="alert"
    >
      <span>{{ error }}</span>
      <UiButton
        variant="secondary"
        size="sm"
        :disabled="loading"
        @click="emit('retry')"
      >
        重试
      </UiButton>
    </p>

    <div
      v-else-if="loading && entries.length === 0"
      class="p-6 text-center text-sm text-[var(--color-text-muted)]"
      role="status"
    >
      加载最近失败请求…
    </div>

    <p
      v-else-if="entries.length === 0"
      class="p-6 text-center text-sm text-[var(--color-text-muted)]"
      data-testid="failure-feed-empty"
    >
      当前窗口没有失败请求。
    </p>

    <ul
      v-else
      class="divide-y divide-[var(--color-border-subtle)]"
      data-testid="failure-feed-list"
    >
      <li
        v-for="entry in entries"
        :key="`${entry.log.request_id}-${entry.log.created_at}`"
      >
        <button
          type="button"
          class="block w-full px-4 py-2.5 text-left transition-colors duration-[var(--duration-micro)] hover:bg-[color-mix(in_srgb,var(--color-hover)_40%,transparent)]"
          :title="entry.log.model_id ? `按模型 ${entry.log.model_id} 过滤请求明细` : undefined"
          @click="selectFailure(entry.log)"
        >
          <div class="flex items-baseline justify-between gap-3">
            <span class="font-mono-data text-xs text-[var(--color-text-muted)]">{{ entry.time }}</span>
            <span class="truncate font-mono-data text-xs font-medium text-[var(--color-text)]">{{ entry.log.model_id ?? entry.log.endpoint }}</span>
          </div>
          <div class="mt-1 flex items-baseline justify-between gap-3">
            <span class="truncate text-xs text-[var(--color-danger)]">{{ entry.log.error_code ?? `HTTP ${entry.log.http_status}` }}</span>
            <span class="shrink-0 font-mono-data text-xs text-[var(--color-text-muted)]">{{ formatAverageLatency(entry.log.duration_ms) }}</span>
          </div>
        </button>
      </li>
    </ul>
  </section>
</template>
