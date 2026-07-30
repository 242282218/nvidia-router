<script setup lang="ts">
import { reactive, watch } from 'vue'

import type { RuntimeSettings } from './types'

interface SettingsFields {
  queue_capacity: number
  queue_wait_timeout_seconds: number
  connect_timeout_seconds: number
  first_byte_timeout_seconds: number
  nonstream_total_timeout_minutes: number
  shutdown_grace_seconds: number
}

type SettingParam = keyof RuntimeSettings

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

watch(() => props.settings, (settings) => {
  if (!settings) return
  fields.queue_capacity = settings.queue_capacity
  fields.queue_wait_timeout_seconds = settings.queue_wait_timeout_ms / 1000
  fields.connect_timeout_seconds = settings.connect_timeout_ms / 1000
  fields.first_byte_timeout_seconds = settings.first_byte_timeout_ms / 1000
  fields.nonstream_total_timeout_minutes = settings.nonstream_total_timeout_ms / 60_000
  fields.shutdown_grace_seconds = settings.shutdown_grace_ms / 1000
}, { immediate: true })

function submit(): void {
  emit('save', {
    queue_capacity: Number(fields.queue_capacity),
    queue_wait_timeout_ms: toMilliseconds(fields.queue_wait_timeout_seconds, 1000),
    connect_timeout_ms: toMilliseconds(fields.connect_timeout_seconds, 1000),
    first_byte_timeout_ms: toMilliseconds(fields.first_byte_timeout_seconds, 1000),
    nonstream_total_timeout_ms: toMilliseconds(fields.nonstream_total_timeout_minutes, 60_000),
    shutdown_grace_ms: toMilliseconds(fields.shutdown_grace_seconds, 1000),
  })
}

function toMilliseconds(value: number, multiplier: number): number {
  return Math.round(Number(value) * multiplier)
}
</script>

<template>
  <form
    data-testid="runtime-settings-form"
    class="rounded-xl border border-slate-800 bg-slate-900 p-5"
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
        >
        <span
          v-if="fieldErrors.queue_capacity"
          data-testid="error-queue_capacity"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.queue_capacity }}</span>
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
        >
        <span
          v-if="fieldErrors.queue_wait_timeout_ms"
          data-testid="error-queue_wait_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.queue_wait_timeout_ms }}</span>
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
        >
        <span
          v-if="fieldErrors.connect_timeout_ms"
          data-testid="error-connect_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.connect_timeout_ms }}</span>
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
        >
        <span
          v-if="fieldErrors.first_byte_timeout_ms"
          data-testid="error-first_byte_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.first_byte_timeout_ms }}</span>
      </label>
      <label class="text-sm text-slate-300">
        非流式总超时（分钟）
        <input
          v-model.number="fields.nonstream_total_timeout_minutes"
          data-testid="nonstream-timeout-minutes"
          class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
          min="0.0167"
          max="30"
          step="0.1"
          type="number"
        >
        <span
          v-if="fieldErrors.nonstream_total_timeout_ms"
          data-testid="error-nonstream_total_timeout_ms"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.nonstream_total_timeout_ms }}</span>
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
        >
        <span
          v-if="fieldErrors.shutdown_grace_ms"
          data-testid="error-shutdown_grace_ms"
          class="mt-1 block text-xs text-rose-300"
        >{{ fieldErrors.shutdown_grace_ms }}</span>
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
