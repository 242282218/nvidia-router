<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError, isRecord } from '../../shared/api/client'
import { formatDate, formatTimeOfDay } from '../../shared/format'
import { modelHealthApi } from '../model-health/api'
import type { ModelHealthModel, ModelHealthSummary } from '../model-health/types'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import UiStatCard from '../../shared/ui/UiStatCard.vue'
import { usePolling } from '../../shared/usePolling'
import { runtimeApi } from './api'
import SettingsForm from './SettingsForm.vue'
import type { RuntimeSettings, RuntimeSummary } from './types'

const summary = ref<RuntimeSummary | null>(null)
const settings = ref<RuntimeSettings | null>(null)
const channelHealth = ref<ModelHealthSummary | null>(null)
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const formError = ref('')
const fieldErrors = ref<Partial<Record<keyof RuntimeSettings, string>>>({})
const savedMessage = ref('')
// summaryUpdatedAt tracks the last successful summary poll so the operator can
// judge how fresh the transient numbers (active/queue/cooldown) are.
const summaryUpdatedAt = ref<Date | null>(null)
let loadSequence = 0
let disposed = false
let loadController: globalThis.AbortController | null = null
let saveController: globalThis.AbortController | null = null

onMounted(() => {
  void loadRuntime()
})

// The summary is transient (active requests, queue depth, cooldowns); refresh
// it on a light poll so a page left open does not go stale. Settings are only
// loaded on mount and after a save. Polling pauses on hidden tabs.
usePolling(() => pollSummary(), 5_000)

// Channel health probes run on the backend cadence; a 30s poll is plenty and
// keeps the extra request off the 5s critical path.
usePolling(() => pollChannelHealth(), 30_000)

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
  loadController?.abort()
  saveController?.abort()
})

// Background summary refresh: transient poll failures keep the last good
// summary instead of flashing an error every cycle.
async function pollSummary(): Promise<void> {
  if (disposed || loading.value || saving.value) return
  try {
    const next = await runtimeApi.getSummary()
    if (disposed) return
    summary.value = next.data
    summaryUpdatedAt.value = new Date()
  } catch {
    // Keep the previous summary; the next poll retries.
  }
}

// Channel-health refresh follows the same keep-last-good contract as the
// summary poll: a transient failure must not blank the card.
async function pollChannelHealth(): Promise<void> {
  if (disposed || loading.value) return
  try {
    const next = await modelHealthApi.getSummary('24h', 'default', 'availability')
    if (disposed) return
    channelHealth.value = next.data
  } catch {
    // Keep the previous snapshot; the next poll retries.
  }
}

async function loadRuntime(): Promise<void> {
  if (disposed) return
  loadController?.abort()
  const controller = new globalThis.AbortController()
  loadController = controller
  const sequence = ++loadSequence
  loading.value = true
  const [summaryResult, settingsResult, healthResult] = await Promise.allSettled([
    runtimeApi.getSummary(controller.signal),
    runtimeApi.getSettings(controller.signal),
    modelHealthApi.getSummary('24h', 'default', 'availability', controller.signal),
  ])
  if (disposed || sequence !== loadSequence) return
  const errors: string[] = []
  if (summaryResult.status === 'fulfilled') {
    summary.value = summaryResult.value.data
    summaryUpdatedAt.value = new Date()
  } else if (!isAbortError(summaryResult.reason)) {
    errors.push(summaryResult.reason instanceof ApiError ? summaryResult.reason.message : '运行状态加载失败。')
  }
  if (settingsResult.status === 'fulfilled') {
    settings.value = settingsResult.value.data
  } else if (!isAbortError(settingsResult.reason)) {
    errors.push(settingsResult.reason instanceof ApiError ? settingsResult.reason.message : '运行设置加载失败。')
  }
  if (healthResult.status === 'fulfilled' && isModelHealthSummary(healthResult.value)) {
    channelHealth.value = healthResult.value.data
  }
  // Channel health is supplementary telemetry: a failure degrades the card to
  // its own empty state rather than failing the whole page load.
  errorMessage.value = errors.join(' ')
  loading.value = false
}

// The model-health API returns unknown-typed JSON through apiRequest; validate
// the envelope before trusting the shapes (matches the statistics view guards).
function isModelHealthSummary(value: unknown): value is { data: ModelHealthSummary } {
  return isRecord(value) && isRecord(value.data) && Array.isArray(value.data.models)
}

// Unchecked models may carry a null-ish success_rate from the API; never let a
// NaN leak into the UI ("—" reads as "no measurement", NaN reads as a bug).
function formatSuccessRate(value: number): string {
  return Number.isFinite(value) ? `${value.toFixed(1)}%` : '—'
}

async function saveSettings(next: RuntimeSettings): Promise<void> {
  if (disposed) return
  saveController?.abort()
  const controller = new globalThis.AbortController()
  saveController = controller
  saving.value = true
  formError.value = ''
  fieldErrors.value = {}
  savedMessage.value = ''
try {
    settings.value = (await runtimeApi.updateSettings(next, controller.signal)).data
    savedMessage.value = '设置已保存。'
  } catch (error) {
    if (!isAbortError(error) && !disposed) applySaveError(error)
    return
  } finally {
    if (!disposed && saveController === controller) saving.value = false
  }
  // The summary is only a visual refresh: a failure here must not turn a
  // successful save into "保存失败" (audit #63). Refresh best-effort and
  // surface the summary error independently.
  await refreshSummary(controller.signal)
}

function applySaveError(error: unknown): void {
  if (error instanceof ApiError && isSettingParam(error.param)) {
    fieldErrors.value = { [error.param]: error.message }
    return
  }
  formError.value = error instanceof ApiError ? error.message : '运行设置保存失败。'
}

// Best-effort summary refresh decoupled from a save's success/failure verdict
// (audit #63). A transient refresh failure only shows a distinct inline note for
// the stale summary; it never flips a successful save into a "保存失败" verdict,
// nor clears the save acknowledgement already rendered.
async function refreshSummary(signal: globalThis.AbortSignal): Promise<void> {
  if (disposed) return
  try {
    summary.value = (await runtimeApi.getSummary(signal)).data
    errorMessage.value = ''
  } catch (error) {
    if (disposed || isAbortError(error)) return
    errorMessage.value = error instanceof ApiError ? error.message : '运行状态刷新失败，请手动刷新。'
  }
}

function isSettingParam(value: string | null): value is keyof RuntimeSettings {
  return value !== null && [
    'queue_capacity',
    'queue_wait_timeout_ms',
    'connect_timeout_ms',
    'first_byte_timeout_ms',
    'nonstream_total_timeout_ms',
    'shutdown_grace_ms',
    'failover_status_codes',
    'request_log_retention_days',
    'max_attempts_per_request',
    'retry_budget_ms',
  ].includes(value)
}

// ── 渠道健康口径（成功率先行、零探测未检）：与 model-health/status.ts 的
// displayStatus 阈值一致。该文件属于未提交的渠道状态 WIP，为让本提交自洽，
// 这里以本地纯函数镜像同一判定；WIP 入库后应合并为单一实现。 ──
type ChannelStatus = 'healthy' | 'degraded' | 'unavailable' | 'unchecked'

function channelStatus(model: Pick<ModelHealthModel, 'status' | 'probe_count' | 'skipped_count' | 'success_count' | 'failure_count' | 'timeout_count' | 'success_rate'>): ChannelStatus {
  const effectiveProbes = model.success_count + model.failure_count + model.timeout_count
  if (
    model.status === 'unchecked'
    || model.status === 'stale'
    || model.status === 'unconfigured'
    || model.probe_count <= model.skipped_count
    || effectiveProbes <= 0
    || !Number.isFinite(model.success_rate)
  ) {
    return 'unchecked'
  }
  if (model.success_rate < 50) return 'unavailable'
  if (model.success_rate < 85) return 'degraded'
  return 'healthy'
}

const channelCounts = computed(() => {
  const models = channelHealth.value?.models ?? []
  const counts = { healthy: 0, degraded: 0, unavailable: 0, unchecked: 0 }
  for (const model of models) counts[channelStatus(model)] += 1
  return { ...counts, total: models.length }
})

// Availability-sorted API output is best-first; the runtime card cares about
// problems, so surface the worst non-healthy models at the top.
const problemChannels = computed(() => {
  return (channelHealth.value?.models ?? [])
    .map((model) => ({ model, status: channelStatus(model) }))
    .filter((entry) => entry.status !== 'healthy')
    .slice(0, 5)
})

const channelStatusLabel: Record<ChannelStatus, string> = {
  healthy: '健康',
  degraded: '降级',
  unavailable: '不可用',
  unchecked: '未检',
}

const channelStatusClass: Record<ChannelStatus, string> = {
  healthy: 'bg-[var(--color-success)]',
  degraded: 'bg-[var(--color-warning)]',
  unavailable: 'bg-[var(--color-danger)]',
  unchecked: 'bg-[var(--color-text-subtle)]',
}

// ── Key 池状态分布：状态点 + 数量 + 占总量的进度条。 ──
const keyPoolRows = computed(() => {
  const keys = summary.value?.keys
  if (!keys) return []
  const total = Math.max(keys.total, 1)
  return [
    { label: '就绪', count: keys.ready, color: 'bg-[var(--color-success)]', bar: 'var(--color-success)' },
    { label: '冷却中', count: keys.cooling_down, color: 'bg-[var(--color-warning)]', bar: 'var(--color-warning)' },
    { label: '失效', count: keys.auth_invalid, color: 'bg-[var(--color-danger)]', bar: 'var(--color-danger)' },
    { label: '停用', count: keys.disabled, color: 'bg-[var(--color-text-subtle)]', bar: 'var(--color-text-subtle)' },
  ].map((row) => ({ ...row, percent: Math.round((row.count / total) * 100) }))
})

// ── 运行参数快照：只读 KV，改设置去下方表单。 ──
const settingSnapshots = computed<Array<{ label: string; value: string }>>(() => {
  const current = settings.value
  if (!current) return []
  return [
    { label: '队列容量', value: String(current.queue_capacity) },
    { label: '排队等待超时', value: formatMs(current.queue_wait_timeout_ms) },
    { label: '连接超时', value: formatMs(current.connect_timeout_ms) },
    { label: '首字节超时', value: formatMs(current.first_byte_timeout_ms) },
    { label: '非流式总超时', value: formatMs(current.nonstream_total_timeout_ms) },
    { label: '单请求最大尝试', value: `${current.max_attempts_per_request} 次` },
    { label: '重试预算', value: formatMs(current.retry_budget_ms) },
    { label: '日志保留', value: `${current.request_log_retention_days} 天` },
  ]
})

function formatMs(value: number): string {
  if (value < 1000) return `${value} ms`
  return `${Number((value / 1000).toFixed(value % 1000 === 0 ? 0 : 1))} 秒`
}

function scrollToSettings(): void {
  globalThis.document.getElementById('runtime-settings')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="系统观测"
        title="运行状态"
        subtitle="Key 池就绪、队列与渠道健康的实时总览；运行参数在页底调整。"
      />

      <Transition name="slide">
        <p
          v-if="errorMessage"
          class="mb-4 flex flex-wrap items-center gap-3 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          <span>{{ errorMessage }}</span>
          <UiButton
            variant="secondary"
            size="sm"
            :disabled="loading"
            @click="loadRuntime"
          >
            重试
          </UiButton>
        </p>
      </Transition>

      <div
        v-if="loading"
        role="status"
        aria-busy="true"
        aria-label="加载运行状态…"
      >
        <UiSkeleton
          variant="cards"
          :lines="4"
        />
      </div>

      <template v-else>
        <p
          v-if="summaryUpdatedAt"
          class="mb-3 flex items-center gap-1.5 text-xs text-[var(--color-text-subtle)]"
        >
          <span
            class="h-1.5 w-1.5 rounded-full bg-[var(--color-success)] pulse-dot"
            aria-hidden="true"
          />
          每 5 秒自动刷新 · 更新于 {{ formatTimeOfDay(summaryUpdatedAt) }}
        </p>

        <!-- KPI 行：CPA 平铺六卡（设计 §4.1），tone 只落在图标块与局部强调。 -->
        <div
          v-if="summary"
          class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
          data-testid="runtime-kpi-row"
        >
          <UiStatCard
            data-testid="runtime-key-counts"
            icon="key"
            tone="success"
            label="Key 就绪"
            :value="`${summary.keys.ready} / ${summary.keys.total}`"
            :hint="`启用 ${summary.keys.enabled} · 停用 ${summary.keys.disabled}`"
          />
          <UiStatCard
            data-testid="runtime-active"
            icon="bolt"
            tone="info"
            label="活跃请求"
            :value="summary.active"
            hint="当前处理中的上游请求"
          />
          <UiStatCard
            data-testid="runtime-queue"
            icon="boxes"
            tone="default"
            label="队列深度"
            :value="`${summary.queue.length} / ${summary.queue.capacity}`"
            hint="等待接纳的请求 / 容量"
          />
          <UiStatCard
            data-testid="runtime-cooldown"
            icon="clock"
            tone="warning"
            label="冷却中"
            :value="summary.keys.cooling_down"
            :hint="summary.earliest_cooldown ? `最早结束 ${formatDate(summary.earliest_cooldown)}` : '当前无冷却'"
          />
          <UiStatCard
            data-testid="runtime-channel-health"
            icon="heartbeat"
            tone="success"
            label="渠道健康"
            :value="channelCounts.total > 0 ? `${channelCounts.healthy} / ${channelCounts.total}` : '—'"
            :hint="`降级 ${channelCounts.degraded} · 不可用 ${channelCounts.unavailable} · 未检 ${channelCounts.unchecked}`"
          />
          <UiStatCard
            data-testid="runtime-shutdown"
            icon="server"
            :tone="summary.shutting_down ? 'warning' : 'success'"
            :tone-value="summary.shutting_down"
            label="服务状态"
            :value="summary.shutting_down ? '关闭中' : '接收请求'"
            :hint="summary.shutting_down ? '拒绝新请求，等待在途完成' : '正常对外服务'"
          />
        </div>

        <!-- 中排三卡：Key 池分布 / 渠道健康 / 运行参数快照。 -->
        <div class="mt-3 grid gap-3 xl:grid-cols-3">
          <section
            class="card min-w-0 p-5"
            aria-label="Key 池状态分布"
          >
            <h2 class="type-heading">
              Key 池状态分布
            </h2>
            <p class="mt-1 text-xs text-[var(--color-text-muted)]">
              按总量 {{ summary?.keys.total ?? '—' }} 把的占比展示。
            </p>
            <ul
              v-if="keyPoolRows.length"
              class="mt-4 space-y-4"
            >
              <li
                v-for="row in keyPoolRows"
                :key="row.label"
                data-testid="runtime-key-pool-row"
              >
                <div class="flex items-center gap-2 text-sm">
                  <span
                    class="h-2 w-2 shrink-0 rounded-full"
                    :class="row.color"
                    aria-hidden="true"
                  />
                  <span class="text-[var(--color-text-secondary)]">{{ row.label }}</span>
                  <span class="ml-auto font-mono-data font-medium text-[var(--color-text)]">{{ row.count }}</span>
                  <span class="w-10 text-right font-mono-data text-xs text-[var(--color-text-subtle)]">{{ row.percent }}%</span>
                </div>
                <div class="mt-1.5 h-[5px] overflow-hidden rounded-full bg-[var(--color-sunken)]">
                  <div
                    class="h-full rounded-full transition-[width] duration-[var(--duration-local)]"
                    :style="{ width: `${row.percent}%`, background: row.bar }"
                  />
                </div>
              </li>
            </ul>
          </section>

          <section
            class="card flex min-w-0 flex-col p-5"
            aria-label="渠道健康"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <h2 class="type-heading">
                  渠道健康
                </h2>
                <p class="mt-1 text-xs text-[var(--color-text-muted)]">
                  白名单模型 24 小时探测成功率（每 30 秒刷新）。
                </p>
              </div>
              <router-link
                to="/channel-status"
                class="btn-ghost btn-sm shrink-0"
                data-testid="runtime-channel-link"
              >
                渠道状态页
              </router-link>
            </div>
            <template v-if="channelCounts.total > 0">
              <div class="mt-3 flex flex-wrap gap-1.5">
                <span class="badge-success">健康 {{ channelCounts.healthy }}</span>
                <span class="badge-warning">降级 {{ channelCounts.degraded }}</span>
                <span class="badge-danger">不可用 {{ channelCounts.unavailable }}</span>
                <span class="badge-muted">未检 {{ channelCounts.unchecked }}</span>
              </div>
              <ul
                v-if="problemChannels.length"
                class="mt-3 divide-y divide-[var(--color-border-subtle)]"
                data-testid="runtime-channel-problems"
              >
                <li
                  v-for="entry in problemChannels"
                  :key="entry.model.model_id"
                  class="flex items-center gap-2 py-2 text-sm"
                >
                  <span
                    class="h-2 w-2 shrink-0 rounded-full"
                    :class="channelStatusClass[entry.status]"
                    aria-hidden="true"
                  />
                  <span class="min-w-0 flex-1 truncate text-[var(--color-text-secondary)]">{{ entry.model.display_name || entry.model.public_id }}</span>
                  <span class="font-mono-data text-xs text-[var(--color-text-muted)]">{{ formatSuccessRate(entry.model.success_rate) }}</span>
                  <span
                    class="text-xs"
                    :class="entry.status === 'unavailable' ? 'text-[var(--color-danger)]' : 'text-[var(--color-warning)]'"
                  >{{ channelStatusLabel[entry.status] }}</span>
                </li>
              </ul>
              <p
                v-else
                class="mt-3 text-sm text-[var(--color-text-muted)]"
                data-testid="runtime-channel-all-healthy"
              >
                全部渠道健康，无降级或不可用模型。
              </p>
            </template>
            <p
              v-else
              class="mt-4 text-sm text-[var(--color-text-muted)]"
            >
              渠道探测数据尚未就绪。
            </p>
          </section>

          <section
            class="card min-w-0 p-5"
            aria-label="运行参数快照"
          >
            <div class="flex items-start justify-between gap-3">
              <h2 class="type-heading">
                运行参数快照
              </h2>
              <button
                type="button"
                class="btn-ghost btn-sm shrink-0"
                data-testid="runtime-settings-anchor"
                @click="scrollToSettings"
              >
                调整设置
              </button>
            </div>
            <dl
              v-if="settingSnapshots.length"
              class="mt-3 space-y-2"
            >
              <div
                v-for="item in settingSnapshots"
                :key="item.label"
                class="flex items-baseline justify-between gap-3 text-sm"
              >
                <dt class="text-[var(--color-text-muted)]">
                  {{ item.label }}
                </dt>
                <dd class="font-mono-data text-xs font-medium text-[var(--color-text)]">
                  {{ item.value }}
                </dd>
              </div>
            </dl>
            <p
              v-else
              class="mt-3 text-sm text-[var(--color-text-muted)]"
            >
              设置加载中或不可用。
            </p>
          </section>
        </div>

        <!-- Save message -->
        <Transition name="slide">
          <p
            v-if="savedMessage"
            data-testid="runtime-saved"
            class="badge-success mt-4 inline-flex px-3 py-1 text-sm"
          >
            {{ savedMessage }}
          </p>
        </Transition>

        <!-- Settings form -->
        <div
          id="runtime-settings"
          class="mt-3 scroll-mt-4"
        >
          <SettingsForm
            :settings="settings"
            :saving="saving"
            :field-errors="fieldErrors"
            :form-error="formError"
            @save="saveSettings"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
/* The stat-card entrance animates as one group on the loading→loaded state
   change; per-card stagger delays were pure decoration (design-aesthetics
   交互动效 P0#1: every animation must explain a state change). */
.slide-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.slide-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
