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
}

type SettingParam = keyof RuntimeSettings

interface SettingRule {
  field: keyof SettingsFields
  integerInput: boolean
  max: number
  min: number
  multiplier: number
  param: SettingParam
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
})
const localErrors = ref<Partial<Record<SettingParam, string>>>({})

const settingRules: SettingRule[] = [
  { field: 'queue_capacity', integerInput: true, min: 1, max: 10_000, multiplier: 1, param: 'queue_capacity' },
  { field: 'queue_wait_timeout_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'queue_wait_timeout_ms' },
  { field: 'connect_timeout_seconds', integerInput: true, min: 1_000, max: 120_000, multiplier: 1_000, param: 'connect_timeout_ms' },
  { field: 'first_byte_timeout_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'first_byte_timeout_ms' },
  { field: 'nonstream_total_timeout_minutes', integerInput: false, min: 1_000, max: 1_800_000, multiplier: 60_000, param: 'nonstream_total_timeout_ms' },
  { field: 'shutdown_grace_seconds', integerInput: true, min: 1_000, max: 600_000, multiplier: 1_000, param: 'shutdown_grace_ms' },
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
}, { immediate: true })

function submit(): void {
  const settings = validateFields()
  if (settings) emit('save', settings)
}

function validateFields(): RuntimeSettings | null {
  const errors: Partial<Record<SettingParam, string>> = {}
  const settings = {} as RuntimeSettings
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
    class="rounded-xl border border-slate-800 bg-slate-900 p-5"
    novalidate
    @submit.prevent="submit"
  >
    <div>
      <h2 class="font-medium">
        运行设置
      </h2>
      <p class="mt-1 text-sm text-slate-400">
        新请求立即使用更新后的队列和超时配置。
      </p>
    </div>
    <div class="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <label class="text-sm text-slate-300">
        队列容量
        <input
          v-model.number="fields.queue_capacity"
          data-testid="queue-capacity"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="1"
          max="10000"
          step="1"
          type="number"
          :aria-invalid="Boolean(fieldError('queue_capacity'))"
        >
        <span
          v-if="fieldError('queue_capacity')"
          data-testid="error-queue_capacity"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('queue_capacity') }}</span>
      </label>
      <label class="text-sm text-slate-300">
        队列等待（秒）
        <input
          v-model.number="fields.queue_wait_timeout_seconds"
          data-testid="queue-wait-seconds"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="1"
          max="600"
          step="1"
          type="number"
          :aria-invalid="Boolean(fieldError('queue_wait_timeout_ms'))"
        >
        <span
          v-if="fieldError('queue_wait_timeout_ms')"
          data-testid="error-queue_wait_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('queue_wait_timeout_ms') }}</span>
      </label>
      <label class="text-sm text-slate-300">
        连接超时（秒）
        <input
          v-model.number="fields.connect_timeout_seconds"
          data-testid="connect-timeout-seconds"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="1"
          max="120"
          step="1"
          type="number"
          :aria-invalid="Boolean(fieldError('connect_timeout_ms'))"
        >
        <span
          v-if="fieldError('connect_timeout_ms')"
          data-testid="error-connect_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('connect_timeout_ms') }}</span>
      </label>
      <label class="text-sm text-slate-300">
        首字节超时（秒）
        <input
          v-model.number="fields.first_byte_timeout_seconds"
          data-testid="first-byte-timeout-seconds"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="1"
          max="600"
          step="1"
          type="number"
          :aria-invalid="Boolean(fieldError('first_byte_timeout_ms'))"
        >
        <span
          v-if="fieldError('first_byte_timeout_ms')"
          data-testid="error-first_byte_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('first_byte_timeout_ms') }}</span>
      </label>
      <label class="text-sm text-slate-300">
        非流式总超时（分钟）
        <input
          v-model.number="fields.nonstream_total_timeout_minutes"
          data-testid="nonstream-timeout-minutes"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="0.016666666666666666"
          max="30"
          step="any"
          type="number"
          :aria-invalid="Boolean(fieldError('nonstream_total_timeout_ms'))"
        >
        <span
          v-if="fieldError('nonstream_total_timeout_ms')"
          data-testid="error-nonstream_total_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('nonstream_total_timeout_ms') }}</span>
      </label>
      <label class="text-sm text-slate-300">
        关闭宽限期（秒）
        <input
          v-model.number="fields.shutdown_grace_seconds"
          data-testid="shutdown-grace-seconds"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="1"
          max="600"
          step="1"
          type="number"
          :aria-invalid="Boolean(fieldError('shutdown_grace_ms'))"
        >
        <span
          v-if="fieldError('shutdown_grace_ms')"
          data-testid="error-shutdown_grace_ms"
          class="mt-1 block text-xs text-rose-300"
          role="alert"
        >{{ fieldError('shutdown_grace_ms') }}</span>
      </label>
    </div>
    <p
      v-if="formError"
      data-testid="runtime-settings-error"
      class="mt-3 text-sm text-rose-300"
      role="alert"
    >
      {{ formError }}
    </p>
    <div class="mt-5 flex justify-end">
      <button
        class="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        type="submit"
        :disabled="saving || !settings"
      >
        保存设置
      </button>
    </div>
  </form>
</template>
