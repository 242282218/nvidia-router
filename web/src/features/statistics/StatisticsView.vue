<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError, isFiniteNumber, isRecord } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import UiStat from '../../shared/ui/UiStat.vue'
import { formatTimeOfDay } from '../../shared/format'
import { usePolling } from '../../shared/usePolling'
import CostPanel from './CostPanel.vue'
import { formatAverageLatency, formatInteger, formatPercent, formatTokens } from './format'
import MonitoringFilterForm from './MonitoringFilterForm.vue'
import MonitoringLogsTable from './MonitoringLogsTable.vue'
import MonitoringTrendChart from './MonitoringTrendChart.vue'
import { statisticsApi } from './api'
import type {
  MonitoringFilter,
  MonitoringRange,
  MonitoringSeriesPoint,
  MonitoringSnapshot,
  RequestLogsPage,
} from './types'

withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })

const ranges: Array<{ value: MonitoringRange; label: string }> = [
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

const range = ref<MonitoringRange>('24h')
const appliedFilters = ref<MonitoringFilter>({})
const snapshot = ref<MonitoringSnapshot | null>(null)
const logs = ref<RequestLogsPage | null>(null)
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

const rangeLabel = computed(() => {
  const found = ranges.find((option) => option.value === range.value)
  return found?.label ?? ''
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
  return successRate <= 100
    && successCount + failureCount === requestCount
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
  return ['model_id', 'error_code', 'upstream_request_id']
    .every((field) => value[field] === undefined || typeof value[field] === 'string')
    && ['access_key_id', 'nvidia_key_id', 'first_byte_ms', 'first_token_ms', 'prompt_tokens', 'completion_tokens']
      .every((field) => value[field] === undefined || (isFiniteNumber(value[field]) && value[field] >= 0))
}

function isMonitoringRange(value: unknown): value is MonitoringRange {
  return value === '24h' || value === '7d' || value === '30d'
}
</script>

<template>
  <div :class="embedded ? '' : 'page-container'">
    <div :class="embedded ? '' : 'content-wrapper'">
      <UiPageHeader
        v-if="!embedded"
        eyebrow="系统观测"
        title="监控"
        subtitle="保存请求元数据，不保存请求或响应正文；可按时间和维度定位异常。"
      >
        <template #actions>
          <div
            class="flex flex-wrap items-center gap-2"
            role="group"
            aria-label="监控时间范围"
          >
            <div class="inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-[var(--shadow-xs)]">
              <button
                v-for="option in ranges"
                :key="option.value"
                :data-testid="`range-${option.value}`"
                class="h-8 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color,box-shadow] duration-[var(--duration-micro)]"
                :class="range === option.value ? 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-[var(--shadow-xs)]' : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)]'"
                type="button"
                :aria-pressed="range === option.value"
                @click="selectRange(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
            <UiButton
              variant="ghost"
              :loading="loading"
              loading-label="刷新中…"
              icon="refresh"
              @click="loadDashboard"
            >
              刷新
            </UiButton>
          </div>
        </template>
      </UiPageHeader>

      <!-- Embedded toolbar -->
      <div
        v-if="embedded"
        class="mb-3 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border-subtle)] pb-3"
      >
        <div class="inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-[var(--shadow-xs)]">
          <button
            v-for="option in ranges"
            :key="option.value"
            :data-testid="`range-${option.value}`"
            class="h-8 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color,box-shadow] duration-[var(--duration-micro)]"
            :class="range === option.value ? 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-[var(--shadow-xs)]' : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)]'"
            type="button"
            :aria-pressed="range === option.value"
            @click="selectRange(option.value)"
          >
            {{ option.label }}
          </button>
        </div>
        <UiButton
          variant="ghost"
          size="sm"
          :loading="loading"
          loading-label="刷新中…"
          icon="refresh"
          @click="loadDashboard"
        >
          刷新
        </UiButton>
      </div>
      <p
        v-if="summaryUpdatedAt"
        class="mt-1 text-xs text-[var(--color-text-subtle)]"
      >
        趋势每 30 秒自动刷新 · 更新于 {{ formatTimeOfDay(summaryUpdatedAt) }}
      </p>

      <div
        v-if="loading && !snapshot && !logs"
        class="mt-5"
        role="status"
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

        <section
          v-if="summary"
          class="mt-4"
          aria-labelledby="kpi-heading"
        >
          <p class="sr-only">
            <span id="kpi-heading">关键指标</span>
          </p>
          <!-- One provenance line for the whole grid instead of one per card:
               ten cards repeating "窗口 24 小时" is noise, not evidence. -->
          <p class="mb-2 text-xs text-[var(--color-text-subtle)]">
            口径：窗口内全部请求元数据聚合 · 窗口：{{ rangeLabel }} · 来源：请求元数据
          </p>
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-5">
            <UiStat
              label="请求数"
              :value="formatInteger(summary.request_count)"
            />
            <UiStat
              label="成功率"
              :value="formatPercent(summary.success_rate)"
              tone="success"
            />
            <UiStat
              label="失败数"
              :value="formatInteger(summary.failure_count)"
              tone="danger"
            />
            <UiStat
              label="平均耗时"
              :value="formatAverageLatency(summary.average_duration_ms)"
            />
            <UiStat
              label="首字节"
              :value="formatAverageLatency(summary.average_first_byte_ms)"
              tone="info"
            />
            <UiStat
              label="TTFT P50"
              :value="formatAverageLatency(summary.first_token_p50_ms)"
              tone="info"
            />
            <UiStat
              label="TTFT P95"
              :value="formatAverageLatency(summary.first_token_p95_ms)"
              tone="info"
            />
            <UiStat
              label="平均排队"
              :value="formatAverageLatency(summary.average_queue_ms)"
            />
            <UiStat
              label="总尝试"
              :value="formatInteger(summary.total_attempts)"
            />
            <UiStat
              label="Token"
              :value="formatTokens(summary.prompt_tokens + summary.completion_tokens)"
              :hint="`输入 ${formatTokens(summary.prompt_tokens)} · 输出 ${formatTokens(summary.completion_tokens)}`"
            />
          </div>
        </section>

        <div class="mt-4 grid gap-4 xl:grid-cols-2">
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="requests"
            title="请求趋势"
            :range-label="rangeLabel"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="failures"
            title="失败趋势"
            :range-label="rangeLabel"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="latency"
            title="延迟趋势"
            :range-label="rangeLabel"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="tokens"
            title="Token 趋势"
            :range-label="rangeLabel"
          />
        </div>

        <CostPanel />

        <MonitoringFilterForm
          class="mt-4"
          @apply="applyFilters"
        />

        <section
          data-testid="monitoring-log-table"
          class="card mt-4 overflow-hidden"
        >
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-5 py-4">
            <div>
              <h2 class="type-heading">
                请求明细
              </h2>
              <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
                只包含元数据，保留期按运行设置执行。
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
            @retry="loadDashboard"
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
                  class="input-field h-8 w-auto rounded-[7px] px-2 text-xs"
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
                  class="h-8 min-w-8 rounded-[7px] border text-xs transition-colors duration-[var(--duration-micro)]"
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
                  class="input-field h-8 w-16 rounded-[7px] px-2 text-xs"
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
