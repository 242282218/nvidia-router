<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import { formatInteger, formatLogDate, formatTokens } from './format'
import type { RequestLogsPage } from './types'

// Presentation-only request log detail: mobile cards + desktop table. The
// empty and error states are rendered here so the parent's `monitoring-log-table`
// section stays one coherent block.
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

function outcome(outcome: 'success' | 'failure'): { variant: 'success' | 'danger'; label: string } {
  return outcome === 'success' ? { variant: 'success', label: '成功' } : { variant: 'danger', label: '失败' }
}
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

    <div
      class="hidden overflow-x-auto md:block"
      tabindex="0"
      aria-label="请求明细表，可横向滚动"
    >
      <table class="data-table min-w-[1200px]">
        <caption class="sr-only">
          请求明细，共 {{ formatInteger(logs.total) }} 条
        </caption>
        <thead>
          <tr>
            <th
              class="data-table-th"
              scope="col"
            >
              时间
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              请求 ID
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              接口 / 模型
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              Key
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              状态
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              流式
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              思考 / 路由
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              排队 / 首字节
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              耗时
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              重试
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              Token
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              错误 / 上游 ID
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="item in logs.items"
            :key="item.request_id"
            class="data-table-row"
          >
            <td class="data-table-td font-mono-data text-xs whitespace-nowrap">
              {{ formatLogDate(item.created_at) }}
            </td>
            <td class="data-table-td font-mono-data text-xs text-[var(--color-info)]">
              {{ item.request_id }}
            </td>
            <td class="data-table-td">
              <span class="block">{{ item.endpoint }}</span>
              <span class="mt-1 block max-w-48 truncate font-mono-data text-xs text-[var(--color-text-muted)]">{{ item.model_id ?? '—' }}</span>
            </td>
            <td class="data-table-td text-xs">
              NVIDIA {{ item.nvidia_key_id ?? '—' }}<br>Access {{ item.access_key_id ?? '—' }}
            </td>
            <td class="data-table-td whitespace-nowrap">
              <span :class="item.outcome === 'success' ? 'text-[var(--color-success)]' : 'text-[var(--color-danger)]'">{{ outcome(item.outcome).label }}</span>
              <span class="ml-1 font-mono-data text-xs">{{ item.http_status }}</span>
            </td>
            <td class="data-table-td">
              {{ item.is_stream ? (item.stream_done ? '是 · [DONE]' : '是 · 未完成') : '否' }}
            </td>
            <td class="data-table-td text-xs">
              <span class="block whitespace-nowrap">请求 {{ item.reasoning_requested ? '是' : '否' }} · 响应 {{ item.reasoning_present ? '是' : '否' }} · {{ item.reasoning_chars ?? '—' }} 字</span>
              <span class="mt-1 block max-w-44 truncate text-[var(--color-text-muted)]">{{ item.reasoning_wire_fields ?? '—' }} · {{ item.route_mode ?? '—' }}</span>
            </td>
            <td class="data-table-td font-mono-data text-xs whitespace-nowrap">
              {{ item.queue_ms }} / {{ item.first_byte_ms ?? '—' }} ms
            </td>
            <td class="data-table-td font-mono-data whitespace-nowrap">
              {{ item.duration_ms }} ms
            </td>
            <td class="data-table-td font-mono-data">
              {{ item.attempt_count }}
            </td>
            <td class="data-table-td font-mono-data text-xs whitespace-nowrap">
              {{ formatTokens((item.prompt_tokens ?? 0) + (item.completion_tokens ?? 0)) }}
            </td>
            <td class="data-table-td max-w-56 truncate text-xs">
              <span class="block text-[var(--color-danger)]">{{ item.error_code ?? '—' }}</span>
              <span class="block truncate text-[var(--color-text-muted)]">{{ item.upstream_request_id ?? '—' }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
