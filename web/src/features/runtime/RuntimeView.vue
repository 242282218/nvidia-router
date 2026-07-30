<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { ApiError } from '../../shared/api/client'
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

onMounted(() => {
  void loadRuntime()
})

async function loadRuntime(): Promise<void> {
  loading.value = true
  try {
    const [summaryResponse, settingsResponse] = await Promise.all([
      runtimeApi.getSummary(),
      runtimeApi.getSettings(),
    ])
    summary.value = summaryResponse.data
    settings.value = settingsResponse.data
    errorMessage.value = ''
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '运行状态加载失败。'
  } finally {
    loading.value = false
  }
}

async function saveSettings(next: RuntimeSettings): Promise<void> {
  saving.value = true
  formError.value = ''
  fieldErrors.value = {}
  savedMessage.value = ''
  try {
    settings.value = (await runtimeApi.updateSettings(next)).data
    savedMessage.value = '设置已保存。'
    summary.value = (await runtimeApi.getSummary()).data
  } catch (error) {
    applySaveError(error)
  } finally {
    saving.value = false
  }
}

function applySaveError(error: unknown): void {
  if (error instanceof ApiError && isSettingParam(error.param)) {
    fieldErrors.value = { [error.param]: error.message }
    return
  }
  formError.value = error instanceof ApiError ? error.message : '运行设置保存失败。'
}

function isSettingParam(value: string | null): value is keyof RuntimeSettings {
  return value !== null && [
    'queue_capacity',
    'queue_wait_timeout_ms',
    'connect_timeout_ms',
    'first_byte_timeout_ms',
    'nonstream_total_timeout_ms',
    'shutdown_grace_ms',
  ].includes(value)
}

function formatDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-4 text-slate-100 sm:p-6">
    <section class="mx-auto max-w-6xl">
      <header class="rounded-xl bg-slate-900 px-5 py-5 shadow-xl sm:px-6">
        <p class="text-sm text-indigo-300">
          运维摘要
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          运行状态
        </h1>
        <p class="mt-2 text-sm text-slate-400">
          查看 Key 池、当前请求和队列状态，并调整运行参数。
        </p>
      </header>

      <p
        v-if="errorMessage"
        class="mt-4 text-sm text-rose-300"
        role="alert"
      >
        {{ errorMessage }}
      </p>
      <div
        v-if="loading"
        class="mt-5 rounded-xl border border-slate-800 bg-slate-900 p-6 text-sm text-slate-400"
      >
        加载中……
      </div>
      <template v-else>
        <section
          v-if="summary"
          class="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-4"
        >
          <article
            data-testid="runtime-key-counts"
            class="rounded-xl border border-slate-800 bg-slate-900 p-5 md:col-span-2"
          >
            <h2 class="text-sm text-slate-400">
              NVIDIA Key
            </h2>
            <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-sm">
              <span>总数 {{ summary.keys.total }}</span>
              <span class="text-emerald-300">就绪 {{ summary.keys.ready }}</span>
              <span>启用 {{ summary.keys.enabled }}</span>
              <span>停用 {{ summary.keys.disabled }}</span>
              <span class="text-amber-300">冷却 {{ summary.keys.cooling_down }}</span>
              <span class="text-rose-300">失效 {{ summary.keys.auth_invalid }}</span>
            </div>
          </article>
          <article
            data-testid="runtime-active"
            class="rounded-xl border border-slate-800 bg-slate-900 p-5"
          >
            <h2 class="text-sm text-slate-400">
              活跃请求
            </h2>
            <p class="mt-2 text-3xl font-semibold">
              {{ summary.active }}
            </p>
          </article>
          <article
            data-testid="runtime-queue"
            class="rounded-xl border border-slate-800 bg-slate-900 p-5"
          >
            <h2 class="text-sm text-slate-400">
              队列 / 容量
            </h2>
            <p class="mt-2 text-3xl font-semibold">
              {{ summary.queue.length }} / {{ summary.queue.capacity }}
            </p>
          </article>
          <article
            data-testid="runtime-cooldown"
            class="rounded-xl border border-slate-800 bg-slate-900 p-5 md:col-span-2"
          >
            <h2 class="text-sm text-slate-400">
              最早冷却结束
            </h2>
            <p class="mt-2 font-mono text-sm">
              {{ formatDate(summary.earliest_cooldown) }}
            </p>
          </article>
          <article
            data-testid="runtime-shutdown"
            class="rounded-xl border border-slate-800 bg-slate-900 p-5 md:col-span-2"
          >
            <h2 class="text-sm text-slate-400">
              服务状态
            </h2>
            <p
              class="mt-2 font-medium"
              :class="summary.shutting_down ? 'text-amber-300' : 'text-emerald-300'"
            >
              {{ summary.shutting_down ? '关闭中' : '接收请求' }}
            </p>
          </article>
        </section>

        <p
          v-if="savedMessage"
          class="mt-4 text-sm text-emerald-300"
        >
          {{ savedMessage }}
        </p>
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
    </section>
  </main>
</template>
