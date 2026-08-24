<script setup lang="ts">
import { formatInteger } from './format'
import RequestRowCard from './RequestRowCard.vue'
import type { RequestLogsPage } from './types'

// 请求明细列表（设计 §5.5）：行卡渲染。空态与错误态渲染在这里，保持父级
// `monitoring-log-table` section 是一个完整语义块。原 UiDataTable 的列排序
// 随行卡化移除（CPA 参考明细无排序交互，排序需求由筛选承担）。
defineOptions({ name: 'MonitoringLogsTable' })

defineProps<{
  logs: RequestLogsPage
  logsError: string
  loading: boolean
  /** True when keyword/dimension filters are active: an empty list then means
   * "no match", not "no data ever" — the two need different copy and actions
   * (data-table 契约：筛选空态必须与从未有数据区分). */
  filtered?: boolean
}>()

const emit = defineEmits<{ retry: []; clearFilters: [] }>()
</script>

<template>
  <p
    v-if="logsError"
    class="m-4 flex flex-wrap items-center gap-3 rounded-[var(--radius-control)] border border-[var(--color-danger-background)] bg-[var(--color-danger-background)] p-4 text-sm text-[var(--color-danger)]"
    role="alert"
  >
    <span>{{ logsError }}</span>
    <button
      class="btn-secondary btn-sm"
      type="button"
      :disabled="loading"
      @click="emit('retry')"
    >
      重试
    </button>
  </p>
  <p
    v-else-if="logs.items.length === 0 && filtered"
    data-testid="monitoring-empty-logs"
    class="p-6 text-center text-sm text-[var(--color-text-muted)]"
  >
    当前筛选条件下没有匹配的请求记录。
    <button
      class="ml-1 font-medium text-[var(--color-info)] underline underline-offset-2 hover:opacity-75"
      type="button"
      @click="emit('clearFilters')"
    >
      清除筛选
    </button>
  </p>
  <p
    v-else-if="logs.items.length === 0"
    data-testid="monitoring-empty-logs"
    class="p-6 text-center text-sm text-[var(--color-text-muted)]"
  >
    暂无请求记录。可调整筛选条件或时间范围。
  </p>
  <div
    v-else
    class="space-y-2 p-4"
    role="list"
    :aria-label="`请求明细，本页 ${formatInteger(logs.items.length)} 条`"
  >
    <div
      v-for="item in logs.items"
      :key="item.request_id"
      role="listitem"
    >
      <RequestRowCard :log="item" />
    </div>
  </div>
</template>
