<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { ApiError } from '../../shared/api/client'
import { nvidiaKeysApi } from './api'
import BatchImportDialog from './BatchImportDialog.vue'
import KeyCards from './KeyCards.vue'
import KeyTable from './KeyTable.vue'
import KeyTestDialog from './KeyTestDialog.vue'
import type { ImportResult, KeyTestResult, NVIDIAKey } from './types'

const keys = ref<NVIDIAKey[]>([])
const singleKey = ref('')
const singleResult = ref<ImportResult | null>(null)
const testResults = ref<KeyTestResult[]>([])
const errorMessage = ref('')
const submitting = ref(false)
const loading = ref(false)
const batchOpen = ref(false)
const testDialogOpen = ref(false)
const busyId = ref<number | null>(null)

onMounted(() => {
  void loadKeys()
})

async function loadKeys(): Promise<void> {
  loading.value = true
  try {
    keys.value = (await nvidiaKeysApi.list()).data
    errorMessage.value = ''
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : 'NVIDIA Key 列表加载失败。'
  } finally {
    loading.value = false
  }
}

async function importOne(): Promise<void> {
  const value = singleKey.value
  singleKey.value = ''
  singleResult.value = null
  errorMessage.value = ''
  if (!value.trim()) {
    errorMessage.value = '请输入 NVIDIA Key。'
    return
  }
  submitting.value = true
  try {
    singleResult.value = await nvidiaKeysApi.importOne(value)
    await loadKeys()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : 'NVIDIA Key 导入失败。'
  } finally {
    submitting.value = false
  }
}

async function toggleKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  errorMessage.value = ''
  try {
    await nvidiaKeysApi.setEnabled(key.id, !key.enabled)
    await loadKeys()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '更新 NVIDIA Key 状态失败。'
  } finally {
    busyId.value = null
  }
}

async function testKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  errorMessage.value = ''
  try {
    testResults.value = [await nvidiaKeysApi.test(key.id)]
    testDialogOpen.value = true
    await loadKeys()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : 'NVIDIA Key 测试失败。'
  } finally {
    busyId.value = null
  }
}

async function testAll(): Promise<void> {
  errorMessage.value = ''
  try {
    const response = await nvidiaKeysApi.testAll()
    testResults.value = response.data
    testDialogOpen.value = response.data.length > 0
    await loadKeys()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '批量测试失败。'
  }
}

async function removeKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  errorMessage.value = ''
  try {
    await nvidiaKeysApi.remove(key.id)
    await loadKeys()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '删除 NVIDIA Key 失败。'
  } finally {
    busyId.value = null
  }
}
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-4 text-slate-100 sm:p-6">
    <section class="mx-auto max-w-6xl">
      <header class="rounded-xl bg-slate-900 px-5 py-5 shadow-xl sm:px-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-sm text-indigo-300">
              运维管理
            </p>
            <h1 class="mt-1 text-2xl font-semibold">
              NVIDIA Key
            </h1>
            <p class="mt-2 text-sm text-slate-400">
              管理上游凭据状态。页面只显示脱敏值，不保留 Key 明文。
            </p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button
              data-testid="open-batch-import"
              class="rounded-lg border border-slate-700 px-3 py-2 text-sm hover:border-slate-500"
              type="button"
              @click="batchOpen = true"
            >
              批量导入
            </button>
            <button
              data-testid="test-all-keys"
              class="rounded-lg border border-slate-700 px-3 py-2 text-sm hover:border-slate-500"
              type="button"
              :disabled="loading"
              @click="testAll"
            >
              顺序测试全部
            </button>
          </div>
        </div>
      </header>

      <section class="mt-5 rounded-xl border border-slate-800 bg-slate-900 p-5">
        <h2 class="font-medium">
          单个导入
        </h2>
        <form
          data-testid="single-import-form"
          class="mt-3 flex flex-col gap-3 sm:flex-row"
          @submit.prevent="importOne"
        >
          <input
            v-model="singleKey"
            class="min-w-0 flex-1 rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-sm"
            name="nvidia-key"
            type="password"
            autocomplete="off"
            spellcheck="false"
            placeholder="粘贴 NVIDIA Key，提交后立即清空"
          >
          <button
            class="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
            type="submit"
            :disabled="submitting"
          >
            导入
          </button>
        </form>
        <p
          v-if="singleResult"
          class="mt-3 text-sm"
          :class="singleResult.status === 'imported' ? 'text-emerald-300' : 'text-amber-300'"
        >
          行 {{ singleResult.line ?? 1 }} · {{ singleResult.masked || '—' }} · {{ singleResult.status }}<span v-if="singleResult.reason"> · {{ singleResult.reason }}</span>
        </p>
        <p
          v-if="errorMessage"
          class="mt-3 text-sm text-rose-300"
          role="alert"
        >
          {{ errorMessage }}
        </p>
      </section>

      <p
        data-testid="mobile-batch-hint"
        class="mt-4 text-xs text-slate-500 md:hidden"
      >
        批量启停等高级操作请在桌面端完成。
      </p>
      <section class="mt-4">
        <div
          v-if="loading"
          class="rounded-xl border border-slate-800 bg-slate-900 p-6 text-sm text-slate-400"
        >
          加载中……
        </div>
        <template v-else>
          <KeyTable
            :keys="keys"
            :busy-id="busyId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="removeKey"
          />
          <KeyCards
            :keys="keys"
            :busy-id="busyId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="removeKey"
          />
        </template>
      </section>
    </section>
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
  </main>
</template>
