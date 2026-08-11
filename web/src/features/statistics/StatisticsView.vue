<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'

import { ApiError, isAbortError, isFiniteNumber, isRecord } from '../../shared/api/client'
import CostPanel from './CostPanel.vue'
import MonitoringTrendChart from './MonitoringTrendChart.vue'
import { statisticsApi } from './api'
import type {
  MonitoringFilter,
  MonitoringRange,
  MonitoringSeriesPoint,
  MonitoringSnapshot,
  RequestLog,
  RequestLogsPage,
} from './types'

const ranges: Array<{ value: MonitoringRange; label: string }> = [
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

const range = ref<MonitoringRange>('24h')
const filterFields = reactive({
  search: '',
  model_id: '',
  endpoint: '',
  outcome: '',
  status: '',
  access_key_id: '',
  nvidia_key_id: '',
})
const appliedFilters = ref<MonitoringFilter>({})
const snapshot = ref<MonitoringSnapshot | null>(null)
const logs = ref<RequestLogsPage | null>(null)
const loading = ref(false)
const summaryError = ref('')
const logsError = ref('')
const page = ref(1)
const pageSize = 50
let disposed = false
let loadSequence = 0
let loadController: globalThis.AbortController | null = null

const summary = computed(() => snapshot.value?.summary ?? null)

onMounted(() => {
  void loadDashboard()
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
  loadController?.abort()
})

async function loadDashboard(): Promise<void> {
  if (disposed) return
  loadController?.abort()
  const controller = new globalThis.AbortController()
  loadController = controller
  const sequence = ++loadSequence
  loading.value = true
  snapshot.value = null
  logs.value = null
  summaryError.value = ''
  logsError.value = ''
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
  } catch (error) {
    if (disposed || sequence !== loadSequence || isAbortError(error)) return
    summaryError.value = error instanceof ApiError ? error.message : '监控汇总加载失败。'
  }
}

async function loadLogs(signal: globalThis.AbortSignal, sequence: number): Promise<void> {
  try {
    const response: unknown = await statisticsApi.getLogs(range.value, appliedFilters.value, page.value, pageSize, signal)
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

function submitFilters(): void {
  appliedFilters.value = collectFilters()
  page.value = 1
  void loadDashboard()
}

function collectFilters(): MonitoringFilter {
  const filters: MonitoringFilter = {}
  addTextFilter(filters, 'search', filterFields.search)
  addTextFilter(filters, 'model_id', filterFields.model_id)
  addTextFilter(filters, 'endpoint', filterFields.endpoint)
  if (filterFields.outcome === 'success' || filterFields.outcome === 'failure') filters.outcome = filterFields.outcome
  const status = parsePositiveInteger(filterFields.status)
  const accessKeyID = parsePositiveInteger(filterFields.access_key_id)
  const nvidiaKeyID = parsePositiveInteger(filterFields.nvidia_key_id)
  if (status !== undefined) filters.status = status
  if (accessKeyID !== undefined) filters.access_key_id = accessKeyID
  if (nvidiaKeyID !== undefined) filters.nvidia_key_id = nvidiaKeyID
  return filters
}

function addTextFilter(filters: MonitoringFilter, key: 'search' | 'model_id' | 'endpoint', value: string): void {
  const trimmed = value.trim()
  if (trimmed) filters[key] = trimmed
}

function parsePositiveInteger(value: string): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
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

function formatInteger(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(value)
}

function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

function formatLatency(value: number): string {
  return `${value.toFixed(1)} ms`
}

function formatOptionalLatency(value: number | undefined): string {
  return value === undefined ? '—' : formatLatency(value)
}

function formatTokens(value: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())}`
}

function outcomeLabel(item: RequestLog): string {
  return item.outcome === 'success' ? '成功' : '失败'
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

function isRequestLog(value: unknown): value is RequestLog {
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
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <header class="section-header">
        <div>
          <p class="text-xs font-medium uppercase tracking-wider text-[var(--color-info)]">
            请求观测
          </p>
          <h1 class="page-title mt-1">
            监控
          </h1>
          <p class="page-subtitle">
            保存请求元数据，不保存请求或响应正文；可按时间和维度定位异常。
          </p>
        </div>
        <div
          class="flex flex-wrap items-center gap-2"
          role="group"
          aria-label="监控时间范围"
        >
          <button
            v-for="option in ranges"
            :key="option.value"
            :data-testid="`range-${option.value}`"
            class="btn-secondary"
            :class="range === option.value ? 'border-[var(--color-accent)] bg-[var(--color-active)] text-[var(--color-accent-bright)]' : ''"
            type="button"
            :aria-pressed="range === option.value"
            @click="selectRange(option.value)"
          >
            {{ option.label }}
          </button>
          <button
            class="btn-ghost"
            type="button"
            :disabled="loading"
            @click="loadDashboard"
          >
            {{ loading ? '刷新中…' : '刷新' }}
          </button>
        </div>
      </header>

      <div
        v-if="loading && !snapshot && !logs"
        class="card flex items-center gap-3 p-6 text-sm text-[var(--color-text-muted)]"
      >
        <span
          class="h-4 w-4 animate-spin rounded-full border-2 border-[var(--color-border-strong)] border-t-[var(--color-accent)]"
          aria-hidden="true"
        />
        加载监控数据…
      </div>

      <template v-else>
        <p
          v-if="summaryError"
          data-testid="monitoring-summary-error"
          class="mb-4 rounded-lg border border-[#ef4444]/25 bg-[#ef4444]/10 px-4 py-3 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ summaryError }}
        </p>

        <div
          v-if="summary"
          class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-10"
        >
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              请求数
            </p>
            <p class="mt-2 text-2xl font-semibold text-[var(--color-text)]">
              {{ formatInteger(summary.request_count) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              成功率
            </p>
            <p class="mt-2 text-2xl font-semibold text-[var(--color-success)]">
              {{ formatPercent(summary.success_rate) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              失败数
            </p>
            <p class="mt-2 text-2xl font-semibold text-[var(--color-danger)]">
              {{ formatInteger(summary.failure_count) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              平均耗时
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-text)]">
              {{ formatLatency(summary.average_duration_ms) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              首字节
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-info)]">
              {{ formatLatency(summary.average_first_byte_ms) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              TTFT P50
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-info)]">
              {{ formatOptionalLatency(summary.first_token_p50_ms) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              TTFT P95
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-info)]">
              {{ formatOptionalLatency(summary.first_token_p95_ms) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              平均排队
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-text)]">
              {{ formatLatency(summary.average_queue_ms) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              总尝试
            </p>
            <p class="mt-2 text-2xl font-semibold text-[var(--color-text)]">
              {{ formatInteger(summary.total_attempts) }}
            </p>
          </article>
          <article class="stat-card">
            <p class="text-xs text-[var(--color-text-muted)]">
              Token
            </p>
            <p class="mt-2 text-xl font-semibold text-[var(--color-text)]">
              {{ formatTokens(summary.prompt_tokens + summary.completion_tokens) }}
            </p>
            <p class="mt-1 text-[11px] text-[var(--color-text-subtle)]">
              输入 {{ formatTokens(summary.prompt_tokens) }} · 输出 {{ formatTokens(summary.completion_tokens) }}
            </p>
          </article>
        </div>

        <div class="mt-4 grid gap-4 xl:grid-cols-2">
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="requests"
            title="请求趋势"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="failures"
            title="失败趋势"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="latency"
            title="延迟趋势"
          />
          <MonitoringTrendChart
            :series="snapshot?.series ?? []"
            metric="tokens"
            title="Token 趋势"
          />
        </div>

        <CostPanel />

        <form
          data-testid="monitoring-filters"
          class="card mt-4 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4"
          @submit.prevent="submitFilters"
        >
          <label class="sm:col-span-2">
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">关键词</span>
            <input
              v-model="filterFields.search"
              data-testid="monitoring-search"
              class="input-field mt-1"
              type="search"
              maxlength="128"
              placeholder="请求 ID、模型、接口、错误码"
            >
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">模型</span>
            <input
              v-model="filterFields.model_id"
              class="input-field mt-1"
              type="text"
              maxlength="128"
              placeholder="全部模型"
            >
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">接口</span>
            <input
              v-model="filterFields.endpoint"
              class="input-field mt-1"
              type="text"
              maxlength="128"
              placeholder="全部接口"
            >
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">结果状态</span>
            <select
              v-model="filterFields.outcome"
              data-testid="monitoring-status"
              class="input-field mt-1"
            >
              <option value="">全部状态</option>
              <option value="success">成功</option>
              <option value="failure">失败</option>
            </select>
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">HTTP 状态码</span>
            <input
              v-model="filterFields.status"
              class="input-field mt-1"
              type="number"
              min="100"
              max="599"
              placeholder="全部"
            >
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">Access Key ID</span>
            <input
              v-model="filterFields.access_key_id"
              class="input-field mt-1"
              type="number"
              min="1"
              placeholder="全部"
            >
          </label>
          <label>
            <span class="text-xs font-medium text-[var(--color-text-secondary)]">NVIDIA Key ID</span>
            <input
              v-model="filterFields.nvidia_key_id"
              class="input-field mt-1"
              type="number"
              min="1"
              placeholder="全部"
            >
          </label>
          <div class="flex items-end justify-end sm:col-span-2 lg:col-span-4">
            <button
              class="btn-primary"
              type="submit"
            >
              应用筛选
            </button>
          </div>
        </form>

        <section
          data-testid="monitoring-log-table"
          class="card mt-4 overflow-hidden"
        >
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
            <div>
              <h2 class="text-sm font-semibold text-[var(--color-text)]">
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

          <p
            v-if="logsError"
            class="m-4 rounded-lg border border-[#ef4444]/25 bg-[#ef4444]/10 p-4 text-sm text-[var(--color-danger)]"
            role="alert"
          >
            {{ logsError }}
          </p>
          <p
            v-else-if="logs && logs.items.length === 0"
            data-testid="monitoring-empty-logs"
            class="p-6 text-center text-sm text-[var(--color-text-muted)]"
          >
            暂无请求记录
          </p>
          <template v-else-if="logs">
            <div class="divide-y divide-[var(--color-border)] md:hidden">
              <article
                v-for="item in logs.items"
                :key="`mobile-${item.request_id}`"
                class="space-y-3 p-4"
              >
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0">
                    <p class="font-mono text-xs text-[var(--color-info)]">
                      {{ item.request_id }}
                    </p>
                    <p class="mt-1 truncate text-xs text-[var(--color-text-muted)]">
                      {{ formatDate(item.created_at) }}
                    </p>
                  </div>
                  <span :class="item.outcome === 'success' ? 'badge-success' : 'badge-danger'">{{ outcomeLabel(item) }} · {{ item.http_status }}</span>
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
                      {{ item.is_stream ? '是' : '否' }}
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
                      {{ formatLatency(item.duration_ms) }}
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
                      错误码
                    </dt><dd class="mt-1 truncate text-[var(--color-danger)]">
                      {{ item.error_code ?? '—' }}
                    </dd>
                  </div>
                  <div class="col-span-2">
                    <dt class="text-[var(--color-text-muted)]">
                      上游请求 ID
                    </dt><dd class="mt-1 truncate font-mono">
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
                <thead>
                  <tr>
                    <th class="data-table-th">
                      时间
                    </th>
                    <th class="data-table-th">
                      请求 ID
                    </th>
                    <th class="data-table-th">
                      接口 / 模型
                    </th>
                    <th class="data-table-th">
                      Key
                    </th>
                    <th class="data-table-th">
                      状态
                    </th>
                    <th class="data-table-th">
                      流式
                    </th>
                    <th class="data-table-th">
                      排队 / 首字节
                    </th>
                    <th class="data-table-th">
                      耗时
                    </th>
                    <th class="data-table-th">
                      重试
                    </th>
                    <th class="data-table-th">
                      Token
                    </th>
                    <th class="data-table-th">
                      错误 / 上游 ID
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="item in logs.items"
                    :key="item.request_id"
                    class="transition-colors hover:bg-[var(--color-hover)]"
                  >
                    <td class="data-table-td whitespace-nowrap font-mono text-xs">
                      {{ formatDate(item.created_at) }}
                    </td>
                    <td class="data-table-td font-mono text-xs text-[var(--color-info)]">
                      {{ item.request_id }}
                    </td>
                    <td class="data-table-td">
                      <span class="block">{{ item.endpoint }}</span>
                      <span class="mt-1 block max-w-48 truncate font-mono text-xs text-[var(--color-text-muted)]">{{ item.model_id ?? '—' }}</span>
                    </td>
                    <td class="data-table-td text-xs">
                      NVIDIA {{ item.nvidia_key_id ?? '—' }}<br>Access {{ item.access_key_id ?? '—' }}
                    </td>
                    <td class="data-table-td whitespace-nowrap">
                      <span :class="item.outcome === 'success' ? 'text-[var(--color-success)]' : 'text-[var(--color-danger)]'">{{ outcomeLabel(item) }}</span>
                      <span class="ml-1 font-mono text-xs">{{ item.http_status }}</span>
                    </td>
                    <td class="data-table-td">
                      {{ item.is_stream ? '是' : '否' }}
                    </td>
                    <td class="data-table-td whitespace-nowrap font-mono text-xs">
                      {{ item.queue_ms }} / {{ item.first_byte_ms ?? '—' }} ms
                    </td>
                    <td class="data-table-td whitespace-nowrap font-mono">
                      {{ item.duration_ms }} ms
                    </td>
                    <td class="data-table-td font-mono">
                      {{ item.attempt_count }}
                    </td>
                    <td class="data-table-td whitespace-nowrap font-mono text-xs">
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

            <div class="flex items-center justify-between gap-3 border-t border-[var(--color-border)] px-4 py-3">
              <span class="text-xs text-[var(--color-text-muted)]">第 {{ logs.page }} 页</span>
              <div class="flex gap-2">
                <button
                  class="btn-secondary"
                  type="button"
                  aria-label="上一页"
                  :disabled="page <= 1 || loading"
                  @click="previousPage"
                >
                  上一页
                </button>
                <button
                  v-if="logs.has_more"
                  data-testid="monitoring-next-page"
                  class="btn-secondary"
                  type="button"
                  aria-label="下一页"
                  :disabled="loading"
                  @click="nextPage"
                >
                  下一页
                </button>
              </div>
            </div>
          </template>
        </section>
      </template>
    </div>
  </div>
</template>
