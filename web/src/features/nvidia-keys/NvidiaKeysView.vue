<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastInfo, toastSuccess } from '../../shared/toast'
import { nvidiaKeysApi } from './api'
import BatchImportDialog from './BatchImportDialog.vue'
import KeyCards from './KeyCards.vue'
import KeyTable from './KeyTable.vue'
import KeyTestDialog from './KeyTestDialog.vue'
import { isImportResult } from './types'
import type { ImportResult, KeyTestResult, NVIDIAKey } from './types'

const keys = ref<NVIDIAKey[]>([])
const singleKey = ref('')
const singleResult = ref<ImportResult | null>(null)
const testResults = ref<KeyTestResult[]>([])
// importError belongs to the single-import form only; every other operation
// reports through the global toast host so an error never renders next to an
// unrelated form.
const importError = ref('')
const submitting = ref(false)
const loading = ref(false)
const loadError = ref('')
const testingAll = ref(false)
const batchOpen = ref(false)
const testDialogOpen = ref(false)
const busyId = ref<number | null>(null)
const confirmingId = ref<number | null>(null)
let loadSequence = 0
let disposed = false

onMounted(() => {
  void loadKeys()
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
})

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback
}

async function loadKeys(): Promise<void> {
  if (disposed) return
  const sequence = ++loadSequence
  loading.value = true
  loadError.value = ''
  try {
    const response: unknown = await nvidiaKeysApi.list()
    if (disposed || sequence !== loadSequence) return
    if (!isDataArrayResponse(response, isNvidiaKey)) {
      throw new TypeError('Invalid NVIDIA Key list response.')
    }
    keys.value = response.data
  } catch (error) {
    if (disposed || sequence !== loadSequence) return
    // A failed load must not read as "no keys exist": surface a persistent
    // error with a retry instead of letting the empty state lie.
    loadError.value = errorMessage(error, 'NVIDIA Key 列表加载失败。')
    toastError(loadError.value)
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

function isNvidiaKey(value: unknown): value is NVIDIAKey {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.masked === 'string'
    && typeof value.enabled === 'boolean'
    && typeof value.auth_invalid === 'boolean'
    && isFiniteNumber(value.cooldown_level)
    && isFiniteNumber(value.consecutive_failures)
    && typeof value.created_at === 'string'
    && typeof value.updated_at === 'string'
    && ['cooldown_until', 'cooldown_reason', 'last_success_at', 'last_error_at', 'last_error_code']
      .every((field) => value[field] === undefined || typeof value[field] === 'string')
}

function isKeyTestResult(value: unknown): value is KeyTestResult {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.status === 'string'
    && ['reason', 'request_id']
      .every((field) => value[field] === undefined || typeof value[field] === 'string')
    && (value.models === undefined
      || (Array.isArray(value.models) && value.models.every((model) => typeof model === 'string')))
}

async function importOne(): Promise<void> {
  const value = singleKey.value
  singleKey.value = ''
  singleResult.value = null
  importError.value = ''
  if (!value.trim()) {
    importError.value = '请输入 NVIDIA Key。'
    return
  }
  submitting.value = true
  try {
    const result: unknown = await nvidiaKeysApi.importOne(value)
    if (disposed) return
    if (!isImportResult(result)) {
      throw new TypeError('Invalid NVIDIA Key import response.')
    }
    singleResult.value = result
    await loadKeys()
  } catch (error) {
    if (disposed) return
    importError.value = errorMessage(error, 'NVIDIA Key 导入失败。')
  } finally {
    if (!disposed) submitting.value = false
  }
}

async function toggleKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  try {
    await nvidiaKeysApi.setEnabled(key.id, !key.enabled)
    if (disposed) return
    await loadKeys()
    toastSuccess(`NVIDIA Key ${key.enabled ? '已停用' : '已启用'}。`)
  } catch (error) {
    if (disposed) return
    toastError(errorMessage(error, '更新 NVIDIA Key 状态失败。'))
  } finally {
    if (!disposed) busyId.value = null
  }
}

async function testKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  try {
    const result: unknown = await nvidiaKeysApi.test(key.id)
    if (disposed) return
    if (!isKeyTestResult(result)) {
      throw new TypeError('Invalid NVIDIA Key test response.')
    }
    testResults.value = [result]
    testDialogOpen.value = true
    await loadKeys()
  } catch (error) {
    if (disposed) return
    toastError(errorMessage(error, 'NVIDIA Key 测试失败。'))
  } finally {
    if (!disposed) busyId.value = null
  }
}

async function testAll(): Promise<void> {
  if (testingAll.value) return
  testingAll.value = true
  try {
    const response: unknown = await nvidiaKeysApi.testAll()
    if (disposed) return
    if (!isDataArrayResponse(response, isKeyTestResult)) {
      throw new TypeError('Invalid NVIDIA Key test-all response.')
    }
    testResults.value = response.data
    if (response.data.length === 0) {
      toastInfo('测活完成，没有异常 Key。')
    } else {
      testDialogOpen.value = true
    }
    await loadKeys()
  } catch (error) {
    if (disposed) return
    toastError(errorMessage(error, '批量测活失败。'))
  } finally {
    if (!disposed) testingAll.value = false
  }
}

async function removeKey(key: NVIDIAKey): Promise<void> {
  if (busyId.value === key.id) return
  // Two-step destructive confirmation (design-system consistent; the native
  // window.confirm dialog is visually detached from the app chrome).
  if (confirmingId.value === key.id) {
    confirmingId.value = null
    busyId.value = key.id
    try {
      await nvidiaKeysApi.remove(key.id)
      if (disposed) return
      await loadKeys()
      toastSuccess(`NVIDIA Key ${key.masked} 已删除。`)
    } catch (error) {
      if (disposed) return
      toastError(errorMessage(error, '删除 NVIDIA Key 失败。'))
    } finally {
      if (!disposed) busyId.value = null
    }
    return
  }
  confirmingId.value = key.id
  globalThis.setTimeout(() => {
    if (confirmingId.value === key.id) confirmingId.value = null
  }, 3000)
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <!-- Header -->
      <header class="section-header">
        <div>
          <p class="text-xs font-medium uppercase tracking-wider text-[var(--color-accent)]">
            运维管理
          </p>
          <h1 class="page-title mt-1">
            NVIDIA Key
          </h1>
          <p class="page-subtitle">
            管理上游凭据状态。页面只显示脱敏值，不保留 Key 明文。
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            data-testid="open-batch-import"
            class="btn-secondary rounded-lg px-4 py-2 text-sm"
            type="button"
            @click="batchOpen = true"
          >
            <span class="flex items-center gap-2">
              <svg
                class="h-4 w-4"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"
                />
              </svg>
              批量导入
            </span>
          </button>
          <button
            data-testid="test-all-keys"
            class="btn-primary rounded-lg px-4 py-2 text-sm"
            type="button"
            :disabled="loading || testingAll"
            @click="testAll"
          >
            <span class="flex items-center gap-2">
              <svg
                class="h-4 w-4"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"
                />
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
                />
              </svg>
              {{ testingAll ? '测活中…' : '顺序测活全部' }}
            </span>
          </button>
        </div>
      </header>

      <!-- Single import -->
      <div class="card p-5 animate-slide-up">
        <h2 class="text-sm font-medium text-[var(--color-text)]">
          单个导入
        </h2>
        <form
          data-testid="single-import-form"
          class="mt-3 flex flex-col gap-3 sm:flex-row"
          @submit.prevent="importOne"
        >
          <div class="relative min-w-0 flex-1">
            <input
              v-model="singleKey"
              class="input-field w-full pr-10 font-mono"
              name="nvidia-key"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="粘贴 NVIDIA Key，提交后立即清空"
            >
          </div>
          <button
            class="btn-primary rounded-lg px-5 py-2.5 text-sm whitespace-nowrap"
            type="submit"
            :disabled="submitting"
          >
            {{ submitting ? '导入中…' : '导入' }}
          </button>
        </form>
        <Transition name="fade">
          <div
            v-if="singleResult"
            class="mt-3"
          >
            <span
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm"
              :class="singleResult.status === 'imported' ? 'badge-success' : 'badge-warning'"
            >
              <span>行 {{ singleResult.line ?? 1 }}</span>
              <span class="opacity-40">·</span>
              <span class="font-mono">{{ singleResult.masked || '—' }}</span>
              <span class="opacity-40">·</span>
              <span>{{ singleResult.status }}</span>
              <span
                v-if="singleResult.reason"
                class="opacity-40"
              >· {{ singleResult.reason }}</span>
            </span>
          </div>
        </Transition>
        <Transition name="fade">
          <p
            v-if="importError"
            class="mt-3 text-sm text-[var(--color-danger)]"
            role="alert"
          >
            {{ importError }}
          </p>
        </Transition>
      </div>

      <!-- Mobile hint -->
      <p
        data-testid="mobile-batch-hint"
        class="mt-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-sunken)] px-3 py-2 text-xs text-[var(--color-text-muted)] md:hidden"
      >
        移动端支持逐条启停、单测和删除；批量导入等高级操作请在桌面端或页面右上角完成。
      </p>

      <!-- Key list -->
      <div class="mt-4">
        <div
          v-if="loading"
          class="card flex items-center gap-3 p-6 text-sm text-[var(--color-text-muted)]"
        >
          <svg
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
          加载中…
        </div>
        <div
          v-else-if="loadError"
          data-testid="nvidia-keys-load-error"
          class="card flex flex-wrap items-center justify-between gap-3 p-6 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          <span>{{ loadError }}</span>
          <button
            data-testid="nvidia-keys-retry"
            class="btn-secondary rounded-lg px-3 py-1.5 text-sm"
            type="button"
            :disabled="loading"
            @click="loadKeys"
          >
            {{ loading ? '重试中…' : '重新加载' }}
          </button>
        </div>
        <template v-else>
          <KeyTable
            :keys="keys"
            :busy-id="busyId"
            :confirming-id="confirmingId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="removeKey"
          />
          <KeyCards
            :keys="keys"
            :busy-id="busyId"
            :confirming-id="confirmingId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="removeKey"
          />
        </template>
      </div>
    </div>

    <BatchImportDialog
      :open="batchOpen"
      @close="batchOpen = false"
      @imported="loadKeys"
    />
    <KeyTestDialog
      :open="testDialogOpen"
      :results="testResults"
      @close="testDialogOpen = false"
    />
  </div>
</template>

<style scoped>
.fade-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.fade-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>