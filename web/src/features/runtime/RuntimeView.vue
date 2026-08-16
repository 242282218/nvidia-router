<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError } from '../../shared/api/client'
import { formatDate } from '../../shared/format'
import PageHeader from '../../shared/components/PageHeader.vue'
import LoadingSpinner from '../../shared/components/LoadingSpinner.vue'
import { usePolling } from '../../shared/usePolling'
import { runtimeApi } from './api'
import SettingsForm from './SettingsForm.vue'
import type { RuntimeSettings, RuntimeSummary } from './types'

const summary = ref<RuntimeSummary | null>(null)
const settings = ref<RuntimeSettings | null>(null)
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

function formatClock(value: Date): string {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(value.getHours())}:${pad(value.getMinutes())}:${pad(value.getSeconds())}`
}

async function loadRuntime(): Promise<void> {
  if (disposed) return
  loadController?.abort()
  const controller = new globalThis.AbortController()
  loadController = controller
  const sequence = ++loadSequence
  loading.value = true
  const [summaryResult, settingsResult] = await Promise.allSettled([
    runtimeApi.getSummary(controller.signal),
    runtimeApi.getSettings(controller.signal),
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
  errorMessage.value = errors.join(' ')
  loading.value = false
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
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="运维摘要"
        title="运行状态"
        subtitle="查看 Key 池、当前请求和队列状态，并调整运行参数。"
      />

      <Transition name="slide">
        <p
          v-if="errorMessage"
          class="mb-4 flex flex-wrap items-center gap-3 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          <span>{{ errorMessage }}</span>
          <button
            class="btn-secondary rounded-lg px-3 py-1 text-xs"
            type="button"
            :disabled="loading"
            @click="loadRuntime"
          >
            重试
          </button>
        </p>
      </Transition>

      <div
        v-if="loading"
        class="card p-6"
      >
        <LoadingSpinner label="运行状态加载中…" />
      </div>

      <template v-else>
        <p
          v-if="summaryUpdatedAt"
          class="mb-3 text-xs text-[var(--color-text-subtle)]"
        >
          每 5 秒自动刷新 · 更新于 {{ formatClock(summaryUpdatedAt) }}
        </p>
        <!-- Summary cards -->
        <div
          v-if="summary"
          class="grid gap-4 md:grid-cols-2 xl:grid-cols-4"
        >
          <!-- Key counts -->
          <div
            data-testid="runtime-key-counts"
            class="stat-card md:col-span-2 xl:col-span-2 animate-slide-up"
          >
            <h2 class="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              NVIDIA Key
            </h2>
            <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-sm">
              <span class="text-[var(--color-text-secondary)]">总数 <strong class="text-[var(--color-text)]">{{ summary.keys.total }}</strong></span>
              <span class="text-[var(--color-success)]">就绪 <strong>{{ summary.keys.ready }}</strong></span>
              <span class="text-[var(--color-text-secondary)]">启用 <strong class="text-[var(--color-text)]">{{ summary.keys.enabled }}</strong></span>
              <span class="text-[var(--color-text-secondary)]">停用 <strong class="text-[var(--color-text)]">{{ summary.keys.disabled }}</strong></span>
              <span class="text-[var(--color-warning)]">冷却 <strong>{{ summary.keys.cooling_down }}</strong></span>
              <span class="text-[var(--color-danger)]">失效 <strong>{{ summary.keys.auth_invalid }}</strong></span>
            </div>
          </div>

          <!-- Active requests -->
          <div
            data-testid="runtime-active"
            class="stat-card animate-slide-up"
          >
            <h2 class="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              活跃请求
            </h2>
            <p class="mt-2 text-3xl font-semibold text-[var(--color-text)]">
              {{ summary.active }}
              <span class="ml-2 inline-block h-2 w-2 rounded-full bg-[var(--color-accent)] pulse-dot align-middle" />
            </p>
          </div>

          <!-- Queue -->
          <div
            data-testid="runtime-queue"
            class="stat-card animate-slide-up"
          >
            <h2 class="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              队列 / 容量
            </h2>
            <p class="mt-2 text-3xl font-semibold text-[var(--color-text)]">
              {{ summary.queue.length }} / {{ summary.queue.capacity }}
            </p>
          </div>

          <!-- Earliest cooldown -->
          <div
            data-testid="runtime-cooldown"
            class="stat-card animate-slide-up"
          >
            <h2 class="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              最早冷却结束
            </h2>
            <p class="mt-2 font-mono text-sm text-[var(--color-text-secondary)]">
              {{ formatDate(summary.earliest_cooldown) }}
            </p>
          </div>

          <!-- Shutdown status -->
          <div
            data-testid="runtime-shutdown"
            class="stat-card animate-slide-up"
          >
            <h2 class="text-xs font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
              服务状态
            </h2>
            <p
              class="mt-2 flex items-center gap-2 font-medium"
              :class="summary.shutting_down ? 'text-[var(--color-warning)]' : 'text-[var(--color-success)]'"
            >
              <span
                class="inline-block h-2 w-2 rounded-full pulse-dot"
                :class="summary.shutting_down ? 'bg-[var(--color-warning)]' : 'bg-[var(--color-accent)]'"
              />
              {{ summary.shutting_down ? '关闭中' : '接收请求' }}
            </p>
          </div>
        </div>

        <!-- Save message -->
        <Transition name="slide">
          <p
            v-if="savedMessage"
            data-testid="runtime-saved"
            class="mt-4 text-sm badge-success inline-flex px-3 py-1"
          >
            {{ savedMessage }}
          </p>
        </Transition>

        <!-- Settings form -->
        <div class="mt-5">
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
