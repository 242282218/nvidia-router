<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError, isFiniteNumber, isRecord } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import UiStatCard from '../../shared/ui/UiStatCard.vue'
import { formatTimeOfDay } from '../../shared/format'
import { usePolling } from '../../shared/usePolling'
import FailureFeed from './FailureFeed.vue'
import { formatAverageLatency, formatInteger, formatPercent, formatTokens } from './format'
import HealthTimeline from './HealthTimeline.vue'
import MonitoringFilterPanel from './MonitoringFilterPanel.vue'
import MonitoringLogsTable from './MonitoringLogsTable.vue'
import TrafficChart from './TrafficChart.vue'
import { statisticsApi } from './api'
import type {
  MonitoringFilter,
  MonitoringRange,
  MonitoringSeriesPoint,
  MonitoringSnapshot,
  RequestLogsPage,
} from './types'

const range = ref<MonitoringRange>('24h')
const appliedFilters = ref<MonitoringFilter>({})
const snapshot = ref<MonitoringSnapshot | null>(null)
const logs = ref<RequestLogsPage | null>(null)
const failureLogs = ref<RequestLogsPage['items']>([])
const failureError = ref('')
const loading = ref(false)
const summaryError = ref('')
const logsError = ref('')
// summaryStale marks a background poll failure: the last good data stays on
// screen but must be flagged as not-fresh instead of silently reading as live.
const summaryStale = ref(false)
const summaryStaleSince = ref<Date | null>(null)
const page = ref(1)
const pageSize = ref(50)
const pageSizes = [50, 100, 200] as const
// jumpTarget is the user-entered page number for direct navigation.
const jumpTarget = ref('')
// summaryUpdatedAt marks the last successful summary poll so a long-open page
// shows how fresh the trend data is.
const summaryUpdatedAt = ref<Date | null>(null)
let disposed = false
let loadSequence = 0
let loadController: globalThis.AbortController | null = null

const summary = computed(() => snapshot.value?.summary ?? null)

// True while any dimension filter is applied: an empty log table then means
// "no match" rather than "no data ever", which drives the distinct empty state.
const filtersActive = computed(() => Object.keys(appliedFilters.value).length > 0)

const filterPanelRef = ref<InstanceType<typeof MonitoringFilterPanel> | null>(null)

function clearFilters(): void {
  filterPanelRef.value?.clearFields()
  appliedFilters.value = {}
  page.value = 1
  void loadDashboard()
}

// FailureFeed 下钻：把模型写入筛选草稿并立即应用（模型 + 失败结果）。
function filterByModel(modelId: string): void {
  filterPanelRef.value?.setDraftField('model_id', modelId)
  appliedFilters.value = { ...appliedFilters.value, model_id: modelId, outcome: 'failure' }
  page.value = 1
  void loadDashboard()
  globalThis.requestAnimationFrame(() => {
    globalThis.document.getElementById('monitoring-log-table-section')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

const rangeLabel = computed(() => {
  const found: Array<{ value: MonitoringRange; label: string }> = [
    { value: '24h', label: '24 小时' },
    { value: '7d', label: '7 天' },
    { value: '30d', label: '30 天' },
  ]
  return found.find((option) => option.value === range.value)?.label ?? ''
})

// 结果分布三行（设计 §5.3）：CPA 明确不要 donut，改为进度列表。
const outcomeRows = computed(() => {
  const data = summary.value
  if (!data) return []
  const total = Math.max(data.success_count + data.canceled_count + data.failure_count, 1)
  return [
    { label: '成功', value: data.success_count, percent: (data.success_count / total) * 100, color: 'var(--color-success)' },
    { label: '取消', value: data.canceled_count, percent: (data.canceled_count / total) * 100, color: 'var(--color-warning)' },
    { label: '失败', value: data.failure_count, percent: (data.failure_count / total) * 100, color: 'var(--color-danger)' },
  ]
})

const outcomeTotal = computed(() => {
  const data = summary.value
  if (!data) return 0
  return data.success_count + data.canceled_count + data.failure_count
})

// totalPages and pageNumbers drive the numbered pagination control; ellipsis
// ("…") marks gaps when the range is wide, keeping the control compact.
const totalPages = computed(() => {
  const total = logs.value?.total ?? 0
  return Math.max(1, Math.ceil(total / pageSize.value))
})

const pageNumbers = computed<Array<number | 'ellipsis'>>(() => {
  const current = page.value
  const last = totalPages.value
  const visible = new Set<number>([1, last])
  for (let offset = -1; offset <= 1; offset++) {
    const candidate = current + offset
    if (candidate >= 1 && candidate <= last) visible.add(candidate)
  }
  const ordered = [...visible].sort((a, b) => a - b)
  const result: Array<number | 'ellipsis'> = []
  let previous = 0
  for (const item of ordered) {
    if (previous > 0 && item - previous > 1) result.push('ellipsis')
    result.push(item)
    previous = item
  }
  return result
})

function changePageSize(): void {
  page.value = 1
  void loadDashboard()
}

function jumpToPage(): void {
  const target = Number(jumpTarget.value)
  if (!Number.isInteger(target) || target < 1 || target > totalPages.value) return
  if (target === page.value) {
    jumpTarget.value = ''
    return
  }
  page.value = target
  jumpTarget.value = ''
  void loadDashboard()
}

function selectPage(target: number): void {
  if (target === page.value) return
  page.value = target
  void loadDashboard()
}

onMounted(() => {
  void loadDashboard()
})

// Trends change as requests land; a light poll keeps a long-open page fresh
// without disturbing the log pagination (which the full reload would reset).
// Suspended while the tab is hidden.
usePolling(() => pollSummary(), 30_000)

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
  loadController?.abort()
})

// Background summary-only refresh: transient failures keep the last good data.
async function pollSummary(): Promise<void> {
  if (disposed || loading.value) return
  try {
    const response: unknown = await statisticsApi.getSummary(range.value, appliedFilters.value)
    if (disposed) return
    if (!isMonitoringSnapshot(response)) return
    snapshot.value = response.data
    summaryUpdatedAt.value = new Date()
    summaryStale.value = false
    summaryStaleSince.value = null
  } catch {
    // Keep the previous snapshot; the next poll retries. The stale flag tells
    // the operator the numbers on screen are no longer live.
    if (!summaryStale.value) summaryStaleSince.value = new Date()
    summaryStale.value = true
  }
}

async function loadDashboard(): Promise<void> {
  if (disposed) return
  loadController?.abort()
  const controller = new globalThis.AbortController()
  loadController = controller
  const sequence = ++loadSequence
  loading.value = true
  summaryError.value = ''
  logsError.value = ''
  // A refresh (range/filter/page change) keeps the current content visible
  // until the new data lands, so the page does not flash to an empty loading
  // state and lose context.
  await Promise.all([
    loadSummary(controller.signal, sequence),
    loadLogs(controller.signal, sequence),
    loadFailureFeed(controller.signal, sequence),
  ])
  if (!disposed && sequence === loadSequence) loading.value = false
}

async function loadSummary(signal: globalThis.AbortSignal, sequence: number): Promise<void> {
  try {
    const response: unknown = await statisticsApi.getSummary(range.value, appliedFilters.value, signal)
    if (disposed || sequence !== loadSequence) return
    if (!isMonitoringSnapshot(response)) throw new TypeError('Invalid monitoring summary response.')
    snapshot.value = response.data
    summaryUpdatedAt.value = new Date()
    summaryStale.value = false
    summaryStaleSince.value = null
  } catch (error) {
    if (disposed || sequence !== loadSequence || isAbortError(error)) return
    summaryError.value = error instanceof ApiError ? error.message : '监控汇总加载失败。'
  }
}

async function loadLogs(signal: globalThis.AbortSignal, sequence: number): Promise<void> {
  try {
    const response: unknown = await statisticsApi.getLogs(range.value, appliedFilters.value, page.value, pageSize.value, signal)
    if (disposed || sequence !== loadSequence) return
    if (!isRequestLogsPage(response)) throw new TypeError('Invalid monitoring logs response.')
    logs.value = response.data
  } catch (error) {
    if (disposed || sequence !== loadSequence || isAbortError(error)) return
    logsError.value = error instanceof ApiError ? error.message : '请求明细加载失败。'
  }
}

// 最近失败请求：只取第一页前 8 条，失败不影响主表（单卡独立错误态）。
async function loadFailureFeed(signal: globalThis.AbortSignal, sequence: number): Promise<void> {
  try {
    const response: unknown = await statisticsApi.getLogs(range.value, { outcome: 'failure' }, 1, 8, signal)
    if (disposed || sequence !== loadSequence) return
    if (!isRequestLogsPage(response)) throw new TypeError('Invalid failure feed response.')
    failureLogs.value = response.data.items
    failureError.value = ''
  } catch (error) {
    if (disposed || sequence !== loadSequence || isAbortError(error)) return
    failureError.value = error instanceof ApiError ? error.message : '最近失败请求加载失败。'
  }
}

function selectRange(next: MonitoringRange): void {
  if (range.value === next) return
  range.value = next
  page.value = 1
  void loadDashboard()
}

function applyFilters(filters: MonitoringFilter): void {
  appliedFilters.value = filters
  page.value = 1
  void loadDashboard()
}

function previousPage(): void {
  if (page.value <= 1) return
  page.value -= 1
  void loadDashboard()
}

function nextPage(): void {
  if (!logs.value?.has_more) return
  page.value += 1
  void loadDashboard()
}

function isMonitoringSnapshot(value: unknown): value is { data: MonitoringSnapshot } {
  if (!isRecord(value) || !isRecord(value.data)) return false
  const snapshot = value.data
  return isMonitoringRange(snapshot.range)
    && typeof snapshot.from === 'string'
    && typeof snapshot.to === 'string'
    && isMonitoringSummary(snapshot.summary)
    && Array.isArray(snapshot.series)
    && snapshot.series.every(isMonitoringSeriesPoint)
}

function isMonitoringSummary(value: unknown): boolean {
  if (!isRecord(value)) return false
  const requestCount = value.request_count
  const successCount = value.success_count
  const failureCount = value.failure_count
  const successRate = value.success_rate
  const duration = value.average_duration_ms
  const firstByte = value.average_first_byte_ms
  const firstToken = value.average_first_token_ms
  const queue = value.average_queue_ms
  const attempts = value.total_attempts
  const promptTokens = value.prompt_tokens
  const completionTokens = value.completion_tokens
  const firstTokenP50 = value.first_token_p50_ms
  const firstTokenP95 = value.first_token_p95_ms
  if (!isNonNegativeNumber(requestCount)
    || !isNonNegativeNumber(successCount)
    || !isNonNegativeNumber(failureCount)
    || !isNonNegativeNumber(successRate)
    || !isNonNegativeNumber(duration)
    || !isNonNegativeNumber(firstByte)
    || !isNonNegativeNumber(firstToken)
    || !isNonNegativeNumber(queue)
    || !isNonNegativeNumber(attempts)
    || !isNonNegativeNumber(promptTokens)
    || !isNonNegativeNumber(completionTokens)
    || (firstTokenP50 !== undefined && !isNonNegativeNumber(firstTokenP50))
    || (firstTokenP95 !== undefined && !isNonNegativeNumber(firstTokenP95))) return false
  // Canceled requests (client disconnects, 499) are counted in their own column
  // and are neither a success nor a failure, so they have to be added back for
  // the totals to reconcile. Reasoning models routinely produce them, and
  // without this the whole view would fall into its error state.
  const canceledCount = isNonNegativeNumber(value.canceled_count) ? value.canceled_count : 0
  return successRate <= 100
    && successCount + failureCount + canceledCount === requestCount
}

function isNonNegativeNumber(value: unknown): value is number {
  return isFiniteNumber(value) && value >= 0
}

function isMonitoringSeriesPoint(value: unknown): value is MonitoringSeriesPoint {
  if (!isRecord(value) || typeof value.bucket !== 'string') return false
  const requestCount = value.request_count
  const successCount = value.success_count
  const failureCount = value.failure_count
  if (!isNonNegativeNumber(requestCount) || !isNonNegativeNumber(successCount) || !isNonNegativeNumber(failureCount)) return false
  return isMonitoringSummary({
    request_count: requestCount,
    success_count: successCount,
    failure_count: failureCount,
    canceled_count: value.canceled_count,
    success_rate: requestCount === 0 ? 0 : (successCount / requestCount) * 100,
    average_duration_ms: value.average_duration_ms,
    average_first_byte_ms: value.average_first_byte_ms,
    average_first_token_ms: value.average_first_token_ms,
    average_queue_ms: value.average_queue_ms,
    total_attempts: value.total_attempts,
    prompt_tokens: value.prompt_tokens,
    completion_tokens: value.completion_tokens,
  })
}

function isRequestLogsPage(value: unknown): value is { data: RequestLogsPage } {
  return isRecord(value)
    && isRecord(value.data)
    && isFiniteNumber(value.data.page)
    && Number.isInteger(value.data.page)
    && value.data.page > 0
    && isFiniteNumber(value.data.page_size)
    && value.data.page_size >= 1
    && isFiniteNumber(value.data.total)
    && value.data.total >= 0
    && typeof value.data.has_more === 'boolean'
    && Array.isArray(value.data.items)
    && value.data.items.every(isRequestLog)
}

function isRequestLog(value: unknown): value is import('./types').RequestLog {
  if (!isRecord(value)
    || typeof value.request_id !== 'string'
    || typeof value.endpoint !== 'string'
    || typeof value.http_status !== 'number'
    || !Number.isInteger(value.http_status)
    || typeof value.outcome !== 'string'
    || (value.outcome !== 'success' && value.outcome !== 'failure')
    || typeof value.is_stream !== 'boolean'
    || typeof value.created_at !== 'string') return false
  const numericFields = ['queue_ms', 'duration_ms', 'attempt_count']
  if (!numericFields.every((field) => isFiniteNumber(value[field]) && value[field] >= 0)) return false
  return ['model_id', 'error_code', 'upstream_request_id', 'requested_capabilities']
    .every((field) => value[field] === undefined || typeof value[field] === 'string')
    && ['access_key_id', 'nvidia_key_id', 'first_byte_ms', 'first_token_ms', 'prompt_tokens', 'completion_tokens']
      .every((field) => value[field] === undefined || (isFiniteNumber(value[field]) && value[field] >= 0))
}

function isMonitoringRange(value: unknown): value is MonitoringRange {
  return value === '24h' || value === '7d' || value === '30d'
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="系统观测"
        title="请求监控"
        subtitle="保存请求元数据，不保存请求或响应正文；可按时间和维度定位异常。"
      />

      <MonitoringFilterPanel
        ref="filterPanelRef"
        :range="range"
        :loading="loading"
        data-testid="monitoring-filter-panel"
        @update:range="selectRange"
        @apply="applyFilters"
        @reset="clearFilters"
        @refresh="loadDashboard"
      />

      <p
        v-if="summaryUpdatedAt"
        class="mt-2 text-xs text-[var(--color-text-subtle)]"
      >
        趋势每 30 秒自动刷新 · 更新于 {{ formatTimeOfDay(summaryUpdatedAt) }}
      </p>

      <div
        v-if="loading && !snapshot && !logs"
        class="mt-4"
        role="status"
        aria-busy="true"
        aria-label="加载监控数据…"
      >
        <UiSkeleton
          variant="cards"
          :lines="6"
        />
        <UiSkeleton
          class="mt-4"
          variant="table"
          :lines="5"
        />
      </div>

      <template v-else>
        <p
          v-if="summaryStale"
          data-testid="monitoring-summary-stale"
          class="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-warning)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] px-4 py-3 text-sm text-[var(--color-warning)]"
          role="status"
        >
          <span>汇总自 {{ summaryStaleSince ? formatTimeOfDay(summaryStaleSince) : '最近一次成功' }} 起未更新，后台刷新失败。</span>
          <button
            class="font-medium underline underline-offset-2 hover:opacity-75"
            type="button"
            :disabled="loading"
            @click="loadDashboard"
          >
            立即重试
          </button>
        </p>
        <p
          v-if="summaryError"
          data-testid="monitoring-summary-error"
          class="mt-4 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-danger)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] px-4 py-3 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ summaryError }}
        </p>

        <!-- KPI 行（设计 §5.2）：CPA 平铺八卡，替代旧的"3 主指标 + 次要指标带"。 -->
        <section
          v-if="summary"
          class="mt-4"
          aria-label="关键指标"
        >
          <p class="mb-2 text-xs text-[var(--color-text-subtle)]">
            口径：窗口内全部请求元数据聚合 · 窗口：{{ rangeLabel }} · 来源：请求元数据
          </p>
          <div
            class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
            data-testid="monitoring-kpi-row"
          >
            <UiStatCard
              icon="pulse"
              tone="default"
              label="请求数"
              :value="summary.request_count"
              :format="formatInteger"
              :hint="`总尝试 ${formatInteger(summary.total_attempts)} 次（含换 Key 重试）`"
            />
            <UiStatCard
              icon="check-circle"
              tone="success"
              label="成功率"
              tone-value
              :value="formatPercent(summary.success_rate)"
              :hint="`成功 ${formatInteger(summary.success_count)} · 失败 ${formatInteger(summary.failure_count)}`"
            />
            <UiStatCard
              icon="x-circle"
              tone="danger"
              label="失败数"
              tone-value
              :value="summary.failure_count"
              :hint="`取消 ${formatInteger(summary.canceled_count)}（不计失败）`"
            />
            <UiStatCard
              icon="timer"
              tone="default"
              label="平均耗时"
              :value="formatAverageLatency(summary.average_duration_ms)"
              :hint="`平均排队 ${formatAverageLatency(summary.average_queue_ms)}`"
            />
            <UiStatCard
              icon="trendingUp"
              tone="info"
              label="TTFT P50"
              :value="formatAverageLatency(summary.first_token_p50_ms)"
              hint="首 Token 中位延迟"
            />
            <UiStatCard
              icon="trendingDown"
              tone="info"
              label="TTFT P95"
              :value="formatAverageLatency(summary.first_token_p95_ms)"
              hint="首 Token P95 延迟"
            />
            <UiStatCard
              icon="bolt"
              tone="default"
              label="首字节"
              :value="formatAverageLatency(summary.average_first_byte_ms)"
              hint="响应首字节平均"
            />
            <UiStatCard
              icon="database"
              tone="info"
              label="Token 总量"
              :value="summary.prompt_tokens + summary.completion_tokens"
              :format="formatTokens"
              :hint="`输入 ${formatTokens(summary.prompt_tokens)} · 输出 ${formatTokens(summary.completion_tokens)}`"
            />
          </div>
        </section>

        <!-- 中排三卡：流量趋势 / 健康时间线 / 结果分布。 -->
        <div class="mt-3 grid gap-3 xl:grid-cols-[minmax(0,2fr)_minmax(0,2.2fr)_minmax(260px,1fr)]">
          <TrafficChart
            :series="snapshot?.series ?? []"
            :range-label="rangeLabel"
          />
          <HealthTimeline
            :series="snapshot?.series ?? []"
            :range="range"
            :from="snapshot?.from"
            :to="snapshot?.to"
          />
          <section
            class="card p-4"
            data-testid="monitoring-outcome-list"
            aria-label="请求结果分布"
          >
            <div class="flex flex-wrap items-baseline justify-between gap-2">
              <h3 class="type-heading">
                结果分布
              </h3>
              <span class="font-mono-data text-xs text-[var(--color-text-muted)]">
                共 {{ formatInteger(outcomeTotal) }} 条
              </span>
            </div>
            <p class="mt-1 text-xs text-[var(--color-text-muted)]">
              取消 = 客户端提前断开（如 499），不计入失败。
            </p>
            <ul
              v-if="outcomeRows.length"
              class="mt-4 space-y-4"
            >
              <li
                v-for="row in outcomeRows"
                :key="row.label"
              >
                <div class="flex items-center gap-2 text-sm">
                  <span
                    class="h-2 w-2 shrink-0 rounded-full"
                    :style="{ background: row.color }"
                    aria-hidden="true"
                  />
                  <span class="text-[var(--color-text-secondary)]">{{ row.label }}</span>
                  <span class="ml-auto font-mono-data font-medium text-[var(--color-text)]">{{ formatInteger(row.value) }}</span>
                  <span class="w-12 text-right font-mono-data text-xs text-[var(--color-text-subtle)]">{{ row.percent.toFixed(1) }}%</span>
                </div>
                <div class="mt-1.5 h-[5px] overflow-hidden rounded-full bg-[var(--color-sunken)]">
                  <div
                    class="h-full rounded-full transition-[width] duration-[var(--duration-local)]"
                    :style="{ width: `${Math.min(100, row.percent)}%`, background: row.color }"
                  />
                </div>
              </li>
            </ul>
          </section>
        </div>

        <!-- 下排：最近失败请求。 -->
        <div class="mt-3">
          <FailureFeed
            :logs="failureLogs"
            :error="failureError"
            :loading="loading"
            @retry="loadDashboard"
            @select="filterByModel"
          />
        </div>

        <section
          id="monitoring-log-table-section"
          data-testid="monitoring-log-table"
          class="card mt-3 overflow-hidden scroll-mt-4"
        >
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-5 py-4">
            <div>
              <h2 class="type-heading">
                请求明细
              </h2>
              <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
                只包含元数据，保留期按运行设置执行；点击行展开完整字段。
              </p>
            </div>
            <span
              v-if="logs"
              class="badge-info"
            >{{ formatInteger(logs.total) }} 条</span>
          </div>

          <MonitoringLogsTable
            v-if="logs"
            :logs="logs"
            :logs-error="logsError"
            :loading="loading"
            :filtered="filtersActive"
            @retry="loadDashboard"
            @clear-filters="clearFilters"
          />

          <div
            v-if="logs"
            class="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--color-border)] px-5 py-3"
          >
            <span class="text-xs text-[var(--color-text-muted)]">
              共 {{ formatInteger(logs.total) }} 条 · 第 {{ logs.page }} / {{ totalPages }} 页
            </span>
            <div class="flex flex-wrap items-center gap-1.5">
              <label class="mr-1 flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
                每页
                <select
                  v-model="pageSize"
                  class="input-field h-8 w-auto rounded-[var(--radius-control)] px-2 text-xs"
                  data-testid="monitoring-page-size"
                  @change="changePageSize"
                >
                  <option
                    v-for="size in pageSizes"
                    :key="size"
                    :value="size"
                  >
                    {{ size }}
                  </option>
                </select>
              </label>
              <UiButton
                variant="secondary"
                size="sm"
                aria-label="上一页"
                :disabled="page <= 1 || loading"
                @click="previousPage"
              >
                上一页
              </UiButton>
              <template v-for="(item, index) in pageNumbers">
                <button
                  v-if="item !== 'ellipsis'"
                  :key="item"
                  :data-testid="`monitoring-page-${item}`"
                  class="h-8 min-w-8 rounded-[var(--radius-control)] border text-xs transition-colors duration-[var(--duration-micro)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11 pointer-coarse:min-w-11"
                  :class="item === page
                    ? 'border-[var(--color-border-strong)] bg-[var(--color-active)] font-semibold text-[var(--color-text)]'
                    : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text-secondary)] hover:bg-[var(--color-hover)]'"
                  type="button"
                  :disabled="loading || item === page"
                  :aria-current="item === page ? 'page' : undefined"
                  @click="selectPage(item)"
                >
                  {{ item }}
                </button>
                <span
                  v-else
                  :key="`ellipsis-${index}`"
                  class="px-0.5 text-xs text-[var(--color-text-subtle)]"
                  aria-hidden="true"
                >
                  …
                </span>
              </template>
              <UiButton
                v-if="logs.has_more"
                data-testid="monitoring-next-page"
                variant="secondary"
                size="sm"
                aria-label="下一页"
                :disabled="loading"
                @click="nextPage"
              >
                下一页
              </UiButton>
              <form
                class="flex items-center gap-1.5"
                @submit.prevent="jumpToPage"
              >
                <label
                  class="sr-only"
                  for="monitoring-jump-page"
                >跳转到页码</label>
                <input
                  id="monitoring-jump-page"
                  v-model="jumpTarget"
                  data-testid="monitoring-jump-page"
                  class="input-field h-8 w-16 rounded-[var(--radius-control)] px-2 text-xs"
                  type="number"
                  min="1"
                  :max="totalPages"
                  placeholder="页码"
                >
                <UiButton
                  variant="ghost"
                  size="sm"
                  type="submit"
                  :disabled="loading"
                >
                  跳转
                </UiButton>
              </form>
            </div>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>
