<script setup lang="ts">
import { computed } from 'vue'

import UiBadge from '../../shared/ui/UiBadge.vue'
import type { DataTableColumn } from '../../shared/ui/dataTable'
import UiDataTable from '../../shared/ui/UiDataTable.vue'
import { formatInteger, formatLogDate, formatTokens } from './format'
import type { RequestLog, RequestLogsPage } from './types'

// Presentation-only request log detail: mobile cards + desktop table. The
// empty and error states are rendered here so the parent's `monitoring-log-table`
// section stays one coherent block.
defineOptions({ name: 'MonitoringLogsTable' })

const props = defineProps<{
  logs: RequestLogsPage
  logsError: string
  loading: boolean
  /** True when keyword/dimension filters are active: an empty list then means
   * "no match", not "no data ever" — the two need different copy and actions
   * (data-table 契约：筛选空态必须与从未有数据区分). */
  filtered?: boolean
}>()

const emit = defineEmits<{ retry: []; clearFilters: [] }>()

function outcome(outcome: 'success' | 'failure'): { variant: 'success' | 'danger'; label: string } {
  return outcome === 'success' ? { variant: 'success', label: '成功' } : { variant: 'danger', label: '失败' }
}

const logColumns: DataTableColumn<RequestLog>[] = [
  { key: 'created_at', label: '时间', sortable: true, value: (row) => row.created_at },
  { key: 'request_id', label: '请求 ID' },
  { key: 'endpoint', label: '接口 / 模型' },
  { key: 'keys', label: 'Key' },
  { key: 'status', label: '状态', sortable: true, value: (row) => row.http_status },
  { key: 'stream', label: '流式' },
  { key: 'reasoning', label: '思考 / 路由' },
  { key: 'queue_ms', label: '排队 / 首字节', align: 'right', sortable: true, value: (row) => row.queue_ms },
  { key: 'duration_ms', label: '耗时', align: 'right', sortable: true, value: (row) => row.duration_ms },
  { key: 'attempt_count', label: '重试', align: 'right', sortable: true, value: (row) => row.attempt_count },
  { key: 'tokens', label: 'Token', align: 'right', sortable: true, value: (row) => (row.prompt_tokens ?? 0) + (row.completion_tokens ?? 0) },
  { key: 'error', label: '错误 / 上游 ID' },
]

const logRows = computed(() => props.logs.items)
</script>

<template>
  <p
    v-if="logsError"
    class="m-4 flex flex-wrap items-center gap-3 rounded-lg border border-[color-mix(in_srgb,var(--color-danger)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4 text-sm text-[var(--color-danger)]"
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
  <template v-else>
    <div class="divide-y divide-[var(--color-border-subtle)] md:hidden">
      <article
        v-for="item in logs.items"
        :key="`mobile-${item.request_id}`"
        class="space-y-3 p-4"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="font-mono-data text-xs text-[var(--color-info)]">
              {{ item.request_id }}
            </p>
            <p class="mt-1 truncate text-xs text-[var(--color-text-muted)]">
              {{ formatLogDate(item.created_at) }}
            </p>
          </div>
          <UiBadge
            :variant="outcome(item.outcome).variant"
            :label="`${outcome(item.outcome).label} · ${item.http_status}`"
          />
        </div>
        <p class="truncate text-sm text-[var(--color-text)]">
          {{ item.endpoint }}
        </p>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
          <div>
            <dt class="text-[var(--color-text-muted)]">
              模型
            </dt><dd class="mt-1 truncate">
              {{ item.model_id ?? '—' }}
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              Key ID
            </dt><dd class="mt-1 truncate">
              NVIDIA {{ item.nvidia_key_id ?? '—' }} · Access {{ item.access_key_id ?? '—' }}
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              流式
            </dt><dd class="mt-1">
              {{ item.is_stream ? (item.stream_done ? '是 · [DONE]' : '是 · 未完成') : '否' }}
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              排队 / 首字节
            </dt><dd class="mt-1">
              {{ item.queue_ms }} / {{ item.first_byte_ms ?? '—' }} ms
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              耗时
            </dt><dd class="mt-1">
              {{ item.duration_ms }} ms
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              Token
            </dt><dd class="mt-1">
              {{ formatTokens((item.prompt_tokens ?? 0) + (item.completion_tokens ?? 0)) }}
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              重试
            </dt><dd class="mt-1">
              {{ item.attempt_count }}
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              路由
            </dt><dd class="mt-1 truncate">
              {{ item.route_mode ?? '—' }}
            </dd>
          </div>
          <div class="col-span-2">
            <dt class="text-[var(--color-text-muted)]">
              思考
            </dt><dd class="mt-1 truncate">
              请求 {{ item.reasoning_requested ? '是' : '否' }} · 响应 {{ item.reasoning_present ? '是' : '否' }} · {{ item.reasoning_chars ?? '—' }} 字<template v-if="item.reasoning_wire_fields">
                （{{ item.reasoning_wire_fields }}）
              </template>
            </dd>
          </div>
          <div>
            <dt class="text-[var(--color-text-muted)]">
              错误码
            </dt><dd class="mt-1 truncate text-[var(--color-danger)]">
              {{ item.error_code ?? '—' }}
            </dd>
          </div>
          <div class="col-span-2">
            <dt class="text-[var(--color-text-muted)]">
              上游请求 ID
            </dt><dd class="mt-1 truncate font-mono-data">
              {{ item.upstream_request_id ?? '—' }}
            </dd>
          </div>
        </dl>
      </article>
    </div>

    <div class="hidden p-4 md:block">
      <UiDataTable
        test-id="monitoring-logs-data-table"
        :caption="`请求明细，共 ${formatInteger(logs.total)} 条`"
        :columns="logColumns"
        :rows="logRows"
        :row-key="(row) => row.request_id"
        :loading="loading"
        max-height="560px"
      >
        <template #cell-created_at="{ row }">
          <span class="font-mono-data text-xs whitespace-nowrap">{{ formatLogDate(row.created_at) }}</span>
        </template>
        <template #cell-request_id="{ row }">
          <span class="font-mono-data text-xs text-[var(--color-info)]">{{ row.request_id }}</span>
        </template>
        <template #cell-endpoint="{ row }">
          <span class="block">{{ row.endpoint }}</span>
          <span class="mt-1 block max-w-48 truncate font-mono-data text-xs text-[var(--color-text-muted)]">{{ row.model_id ?? '—' }}</span>
        </template>
        <template #cell-keys="{ row }">
          <span class="text-xs">NVIDIA {{ row.nvidia_key_id ?? '—' }}<br>Access {{ row.access_key_id ?? '—' }}</span>
        </template>
        <template #cell-status="{ row }">
          <span :class="row.outcome === 'success' ? 'text-[var(--color-success)]' : 'text-[var(--color-danger)]'">{{ outcome(row.outcome).label }}</span>
          <span class="ml-1 font-mono-data text-xs">{{ row.http_status }}</span>
        </template>
        <template #cell-stream="{ row }">
          {{ row.is_stream ? (row.stream_done ? '是 · [DONE]' : '是 · 未完成') : '否' }}
        </template>
        <template #cell-reasoning="{ row }">
          <span class="block text-xs whitespace-nowrap">请求 {{ row.reasoning_requested ? '是' : '否' }} · 响应 {{ row.reasoning_present ? '是' : '否' }} · {{ row.reasoning_chars ?? '—' }} 字</span>
          <span class="mt-1 block max-w-44 truncate text-xs text-[var(--color-text-muted)]">{{ row.reasoning_wire_fields ?? '—' }} · {{ row.route_mode ?? '—' }}</span>
        </template>
        <template #cell-queue_ms="{ row }">
          <span class="font-mono-data text-xs whitespace-nowrap">{{ row.queue_ms }} / {{ row.first_byte_ms ?? '—' }} ms</span>
        </template>
        <template #cell-duration_ms="{ row }">
          <span class="font-mono-data whitespace-nowrap">{{ row.duration_ms }} ms</span>
        </template>
        <template #cell-attempt_count="{ row }">
          <span class="font-mono-data">{{ row.attempt_count }}</span>
        </template>
        <template #cell-tokens="{ row }">
          <span class="font-mono-data text-xs whitespace-nowrap">{{ formatTokens((row.prompt_tokens ?? 0) + (row.completion_tokens ?? 0)) }}</span>
        </template>
        <template #cell-error="{ row }">
          <span class="block max-w-56 truncate text-xs text-[var(--color-danger)]">{{ row.error_code ?? '—' }}</span>
          <span class="block max-w-56 truncate text-xs text-[var(--color-text-muted)]">{{ row.upstream_request_id ?? '—' }}</span>
        </template>
      </UiDataTable>
    </div>
  </template>
</template>
