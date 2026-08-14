<script setup lang="ts">
import { reactive, ref, watch } from 'vue'

import type { RuntimeSettings } from './types'

interface SettingsFields {
  queue_capacity: number | string
  queue_wait_timeout_seconds: number | string
  connect_timeout_seconds: number | string
  first_byte_timeout_seconds: number | string
  nonstream_total_timeout_minutes: number | string
  shutdown_grace_seconds: number | string
  request_log_retention_days: number | string
  max_attempts_per_request: number | string
  retry_budget_ms: number | string
  max_streaming_per_key: number | string
  stream_first_token_timeout_seconds: number | string
  stream_idle_timeout_seconds: number | string
  failover_status_codes: string
  latency_routing_enabled: boolean
  embedding_cache_enabled: boolean
  embedding_cache_max_entries: number | string
}

type SettingParam = keyof RuntimeSettings
type NumericSettingParam = Exclude<SettingParam, 'failover_status_codes' | 'latency_routing_enabled' | 'embedding_cache_enabled'>

interface SettingRule {
  field: Exclude<keyof SettingsFields, 'latency_routing_enabled' | 'embedding_cache_enabled' | 'failover_status_codes'>
  hint?: string
  integerInput: boolean
  max: number
  min: number
  multiplier: number
  param: NumericSettingParam
  testId: string
}

const props = defineProps<{
  settings: RuntimeSettings | null
  saving: boolean
  fieldErrors: Partial<Record<SettingParam, string>>
  formError: string
}>()

const emit = defineEmits<{
  save: [settings: RuntimeSettings]
}>()

const fields = reactive<SettingsFields>({
  queue_capacity: 100,
  queue_wait_timeout_seconds: 60,
  connect_timeout_seconds: 10,
  first_byte_timeout_seconds: 60,
  nonstream_total_timeout_minutes: 5,
  shutdown_grace_seconds: 60,
  request_log_retention_days: 30,
  max_attempts_per_request: 5,
  retry_budget_ms: 120_000,
  max_streaming_per_key: 2,
  stream_first_token_timeout_seconds: 60,
  stream_idle_timeout_seconds: 180,
  failover_status_codes: '429,500,502,503,504',
  latency_routing_enabled: false,
  embedding_cache_enabled: false,
  embedding_cache_max_entries: 256,
})
const localErrors = ref<Partial<Record<SettingParam, string>>>({})

// Bounds below are a UX pre-check only; the backend is authoritative (audit #64).
// They must stay in sync with runtimeconfig/snapshot.go Validate(), which mirrors
// the runtime_settings CHECK constraints. The requirement remains enforced
// server-side: a drift here only relaxes or tightens the inline hint, never the
// accepted value.
const settingRules: SettingRule[] = [
  { field: 'queue_capacity', integerInput: true, min: 1, max: 10_000, multiplier: 1, param: 'queue_capacity', testId: 'queue-capacity' },
  { field: 'queue_wait_timeout_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'queue_wait_timeout_ms', testId: 'queue-wait-seconds' },
  { field: 'connect_timeout_seconds', integerInput: true, min: 1_000, max: 120_000, multiplier: 1_000, param: 'connect_timeout_ms', testId: 'connect-timeout-seconds' },
  { field: 'first_byte_timeout_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'first_byte_timeout_ms', testId: 'first-byte-timeout-seconds' },
  { field: 'nonstream_total_timeout_minutes', integerInput: false, min: 1_000, max: 1_800_000, multiplier: 60_000, param: 'nonstream_total_timeout_ms', testId: 'nonstream-timeout-minutes' },
  { field: 'shutdown_grace_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'shutdown_grace_ms', testId: 'shutdown-grace-seconds' },
  { field: 'request_log_retention_days', integerInput: true, min: 30, max: 365, multiplier: 1, param: 'request_log_retention_days', testId: 'request-log-retention-days' },
  { field: 'max_attempts_per_request', hint: '单个客户端请求最多尝试的 NVIDIA Key 数量，允许范围 1-50。', integerInput: true, min: 1, max: 50, multiplier: 1, param: 'max_attempts_per_request', testId: 'max-attempts-per-request' },
  { field: 'retry_budget_ms', hint: '提交前重试阶段的时间上限，允许范围 1000-600000 毫秒；不限制已提交的流式响应。', integerInput: true, min: 1_000, max: 600_000, multiplier: 1, param: 'retry_budget_ms', testId: 'retry-budget-ms' },
  { field: 'max_streaming_per_key', hint: '单个 NVIDIA Key 同时处理的流式请求数，允许范围 1-10。', integerInput: true, min: 1, max: 10, multiplier: 1, param: 'max_streaming_per_key', testId: 'max-streaming-per-key' },
  { field: 'stream_first_token_timeout_seconds', hint: '流式请求等待首个 token 的时间上限，允许范围 1-1800 秒。', integerInput: true, min: 1_000, max: 1_800_000, multiplier: 1_000, param: 'stream_first_token_timeout_ms', testId: 'stream-first-token-timeout-seconds' },
  { field: 'stream_idle_timeout_seconds', hint: '流式响应中相邻两次输出的空闲上限，允许范围 1-1800 秒。', integerInput: true, min: 1_000, max: 1_800_000, multiplier: 1_000, param: 'stream_idle_timeout_ms', testId: 'stream-idle-timeout-seconds' },
  { field: 'embedding_cache_max_entries', hint: '嵌入缓存最多缓存的响应条数，超过后淘汰最久未命中项（LRU）。', integerInput: true, min: 1, max: 10_000, multiplier: 1, param: 'embedding_cache_max_entries', testId: 'embedding-cache-max-entries' },
]

watch(() => props.settings, (settings) => {
  if (!settings) return
  localErrors.value = {}
  fields.queue_capacity = settings.queue_capacity
  fields.queue_wait_timeout_seconds = settings.queue_wait_timeout_ms / 1000
  fields.connect_timeout_seconds = settings.connect_timeout_ms / 1000
  fields.first_byte_timeout_seconds = settings.first_byte_timeout_ms / 1000
  fields.nonstream_total_timeout_minutes = settings.nonstream_total_timeout_ms / 60_000
  fields.shutdown_grace_seconds = settings.shutdown_grace_ms / 1000
  fields.request_log_retention_days = settings.request_log_retention_days
  fields.max_attempts_per_request = settings.max_attempts_per_request
  fields.retry_budget_ms = settings.retry_budget_ms
  fields.max_streaming_per_key = settings.max_streaming_per_key
  fields.stream_first_token_timeout_seconds = settings.stream_first_token_timeout_ms / 1000
  fields.stream_idle_timeout_seconds = settings.stream_idle_timeout_ms / 1000
  fields.failover_status_codes = settings.failover_status_codes
  fields.latency_routing_enabled = settings.latency_routing_enabled
  fields.embedding_cache_enabled = settings.embedding_cache_enabled
  fields.embedding_cache_max_entries = settings.embedding_cache_max_entries
}, { immediate: true })

function submit(): void {
  const settings = validateFields()
  if (settings) emit('save', settings)
}

function validateFields(): RuntimeSettings | null {
  const errors: Partial<Record<SettingParam, string>> = {}
  const settings = {} as RuntimeSettings
  settings.failover_status_codes = fields.failover_status_codes.trim()
  settings.latency_routing_enabled = fields.latency_routing_enabled
  settings.embedding_cache_enabled = fields.embedding_cache_enabled
  for (const rule of settingRules) {
    const raw = fields[rule.field]
    const value = typeof raw === 'string' && raw.trim() === '' ? Number.NaN : Number(raw)
    const converted = value * rule.multiplier
    const valid = Number.isFinite(value)
      && (!rule.integerInput || Number.isInteger(value))
      && Number.isInteger(converted)
      && converted >= rule.min
      && converted <= rule.max
    if (!valid) {
      errors[rule.param] = rule.integerInput
        ? '请输入允许范围内的整数。'
        : '请输入允许范围内的有效数字。'
      continue
    }
    settings[rule.param] = Math.round(converted)
  }
  localErrors.value = errors
  return Object.keys(errors).length === 0 ? settings : null
}

function fieldError(param: SettingParam): string {
  return localErrors.value[param] ?? props.fieldErrors[param] ?? ''
}
</script>

<template>
  <form
    data-testid="runtime-settings-form"
    class="card p-5"
    novalidate
    @submit.prevent="submit"
  >
    <div>
      <h2 class="text-sm font-medium text-[var(--color-text)]">
        运行设置
      </h2>
      <p class="mt-1 text-sm text-[var(--color-text-muted)]">
        新请求立即使用更新后的队列和超时配置。
      </p>
    </div>

    <div class="mt-5 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
      <label
        v-for="(rule) in settingRules"
        :key="rule.param"
        class="block text-sm font-medium text-[var(--color-text-secondary)]"
      >
        <span>{{
          rule.param === 'queue_capacity' ? '队列容量' :
          rule.param === 'queue_wait_timeout_ms' ? '队列等待（秒）' :
          rule.param === 'connect_timeout_ms' ? '连接超时（秒）' :
          rule.param === 'first_byte_timeout_ms' ? '首字节超时（秒）' :
          rule.param === 'nonstream_total_timeout_ms' ? '非流式总超时（分钟）' :
          rule.param === 'shutdown_grace_ms' ? '关闭宽限期（秒）' :
          rule.param === 'request_log_retention_days' ? '请求日志保留（天）' :
          rule.param === 'max_attempts_per_request' ? '单请求最大尝试次数' :
          rule.param === 'retry_budget_ms' ? '重试预算（毫秒）' :
          rule.param === 'stream_first_token_timeout_ms' ? '首 token 超时（秒）' :
          rule.param === 'stream_idle_timeout_ms' ? '流式空闲超时（秒）' :
          rule.param === 'embedding_cache_max_entries' ? '嵌入缓存上限（条）' :
          '单 Key 流式并发上限'
        }}</span>
        <input
          :value="fields[rule.field]"
          :data-testid="rule.testId"
          class="input-field mt-1.5"
          :min="rule.min / rule.multiplier"
          :max="rule.max / rule.multiplier"
          :step="rule.integerInput ? 1 : 'any'"
          type="number"
          :aria-invalid="Boolean(fieldError(rule.param))"
          @input="(e: Event) => { const t = e.target as HTMLInputElement; (fields[rule.field] as string | number) = t.value; }"
        >
        <span
          v-if="rule.hint"
          :data-testid="`hint-${rule.param}`"
          class="mt-1 block text-xs text-[var(--color-text-muted)]"
        >{{ rule.hint }}</span>
        <Transition name="fade">
          <span
            v-if="fieldError(rule.param)"
            :data-testid="`error-${rule.param}`"
            class="mt-1 block text-xs text-[var(--color-danger)]"
            role="alert"
          >{{ fieldError(rule.param) }}</span>
        </Transition>
      </label>

      <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
        <span>故障转移状态码</span>
        <input
          :value="fields.failover_status_codes"
          data-testid="failover-status-codes"
          class="input-field mt-1.5"
          type="text"
          placeholder="429,500-599"
          :aria-invalid="Boolean(fieldError('failover_status_codes'))"
          @input="(e: Event) => { fields.failover_status_codes = (e.target as HTMLInputElement).value }"
        >
        <span class="mt-1 block text-xs text-[var(--color-text-muted)]">使用逗号分隔状态码或范围；留空表示不自动切换。</span>
        <Transition name="fade">
          <span
            v-if="fieldError('failover_status_codes')"
            data-testid="error-failover_status_codes"
            class="mt-1 block text-xs text-[var(--color-danger)]"
            role="alert"
          >{{ fieldError('failover_status_codes') }}</span>
        </Transition>
      </label>

      <label class="flex items-start gap-3 rounded-lg border border-[var(--color-border)] p-3 text-sm">
        <input
          v-model="fields.latency_routing_enabled"
          data-testid="latency-routing-enabled"
          class="mt-0.5 h-4 w-4 rounded border-[var(--color-text-subtle)]"
          type="checkbox"
        >
        <span>
          <span class="font-medium text-[var(--color-text)]">质量感知调度</span>
          <span class="mt-0.5 block text-xs text-[var(--color-text-muted)]">按真实请求质量优先、请求延迟辅助选择出口；未充分采样的出口低频探索，关闭则恢复纯轮转。</span>
        </span>
      </label>

      <label class="flex items-start gap-3 rounded-lg border border-[var(--color-border)] p-3 text-sm">
        <input
          v-model="fields.embedding_cache_enabled"
          data-testid="embedding-cache-enabled"
          class="mt-0.5 h-4 w-4 rounded border-[var(--color-text-subtle)]"
          type="checkbox"
        >
        <span>
          <span class="font-medium text-[var(--color-text)]">嵌入精确匹配缓存</span>
          <span class="mt-0.5 block text-xs text-[var(--color-text-muted)]">对完全相同的嵌入输入直接返回缓存向量，跳过上游调用；命中不计入上游用量。</span>
        </span>
      </label>
    </div>

    <Transition name="slide">
      <p
        v-if="formError"
        data-testid="runtime-settings-error"
        class="mt-3 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ formError }}
      </p>
    </Transition>

    <div class="mt-5 flex justify-end">
      <button
        class="btn-primary rounded-lg px-5 py-2.5 text-sm"
        type="submit"
        :disabled="saving || !settings"
      >
        <span class="flex items-center gap-2">
          <svg
            v-if="saving"
            class="h-4 w-4 animate-spin"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            />
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
            />
          </svg>
          {{ saving ? '保存中…' : '保存设置' }}
        </span>
      </button>
    </div>
  </form>
</template>

<style scoped>
.fade-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.fade-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
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
