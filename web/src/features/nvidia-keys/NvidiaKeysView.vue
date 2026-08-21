<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastInfo, toastSuccess } from '../../shared/toast'
import { useAsyncData } from '../../shared/useAsyncData'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import UiCard from '../../shared/ui/UiCard.vue'
import UiConfirmDialog from '../../shared/ui/UiConfirmDialog.vue'
import UiField from '../../shared/ui/UiField.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import { nvidiaKeysApi } from './api'
import BatchImportDialog from './BatchImportDialog.vue'
import KeyCards from './KeyCards.vue'
import KeyTable from './KeyTable.vue'
import KeyTestDialog from './KeyTestDialog.vue'
import { isImportResult } from './types'
import type { ImportResult, KeyTestResult, NVIDIAKey } from './types'

const { data: keys, loading, error: loadError, refresh: loadKeys, isDisposed } = useAsyncData<NVIDIAKey[]>(
  async () => {
    const response: unknown = await nvidiaKeysApi.list()
    if (!isDataArrayResponse(response, isNvidiaKey)) {
      throw new TypeError('Invalid NVIDIA Key list response.')
    }
    return response.data
  },
  { errorMessage: 'NVIDIA Key 列表加载失败。' },
)

const keyList = computed<NVIDIAKey[]>(() => keys.value ?? [])

// Test results only carry the numeric id; the dialog labels each result with
// the masked value from the already-loaded key list so batch results stay
// attributable (Key #id alone is meaningless after a test-all run).
const maskedById = computed<Map<number, string>>(() => {
  const map = new Map<number, string>()
  for (const key of keyList.value) map.set(key.id, key.masked)
  return map
})

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback
}

const singleKey = ref('')
const singleResult = ref<ImportResult | null>(null)
const testResults = ref<KeyTestResult[]>([])
// importError belongs to the single-import form only; every other operation
// reports through the global toast host so an error never renders next to an
// unrelated form.
const importError = ref('')
const submitting = ref(false)
const testingAll = ref(false)
const batchOpen = ref(false)
const testDialogOpen = ref(false)
const busyId = ref<number | null>(null)
// 删除确认：对话框持有待删目标，confirm 才真正执行。
const pendingDelete = ref<NVIDIAKey | null>(null)
// 批量删除走独立确认：消息按数量生成，避免误删一批。
const pendingBatchDelete = ref<NVIDIAKey[] | null>(null)
const deleting = ref(false)
const batchBusy = ref(false)

const route = (() => {
  try { return useRoute() } catch { return null as unknown as ReturnType<typeof useRoute> }
})()
const router = (() => {
  try { return useRouter() } catch { return null as unknown as ReturnType<typeof useRouter> }
})()

onMounted(() => {
  void loadKeys()
  // 命令面板深链：/nvidia-keys?import=1
  if (route?.query.import === '1') {
    batchOpen.value = true
    void router?.replace({ query: { ...(route.query as Record<string, string>), import: undefined } })
  }
})

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
    if (isDisposed()) return
    if (!isImportResult(result)) {
      throw new TypeError('Invalid NVIDIA Key import response.')
    }
    singleResult.value = result
    await loadKeys()
  } catch (error) {
    if (isDisposed()) return
    importError.value = errorMessage(error, 'NVIDIA Key 导入失败。')
  } finally {
    if (!isDisposed()) submitting.value = false
  }
}

async function toggleKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  try {
    await nvidiaKeysApi.setEnabled(key.id, !key.enabled)
    if (isDisposed()) return
    await loadKeys()
    toastSuccess(`NVIDIA Key ${key.enabled ? '已停用' : '已启用'}。`)
  } catch (error) {
    if (isDisposed()) return
    toastError(errorMessage(error, '更新 NVIDIA Key 状态失败。'))
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

async function testKey(key: NVIDIAKey): Promise<void> {
  busyId.value = key.id
  try {
    const result: unknown = await nvidiaKeysApi.test(key.id)
    if (isDisposed()) return
    if (!isKeyTestResult(result)) {
      throw new TypeError('Invalid NVIDIA Key test response.')
    }
    testResults.value = [result]
    testDialogOpen.value = true
    await loadKeys()
  } catch (error) {
    if (isDisposed()) return
    toastError(errorMessage(error, 'NVIDIA Key 测试失败。'))
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

async function testAll(): Promise<void> {
  if (testingAll.value) return
  testingAll.value = true
  try {
    const response: unknown = await nvidiaKeysApi.testAll()
    if (isDisposed()) return
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
    if (isDisposed()) return
    toastError(errorMessage(error, '批量测活失败。'))
  } finally {
    if (!isDisposed()) testingAll.value = false
  }
}

async function confirmDelete(): Promise<void> {
  const key = pendingDelete.value
  if (!key || deleting.value) return
  deleting.value = true
  try {
    await nvidiaKeysApi.remove(key.id)
    if (isDisposed()) return
    pendingDelete.value = null
    await loadKeys()
    toastSuccess(`NVIDIA Key ${key.masked} 已删除。`)
  } catch (error) {
    if (isDisposed()) return
    toastError(errorMessage(error, '删除 NVIDIA Key 失败。'))
  } finally {
    if (!isDisposed()) deleting.value = false
  }
}

// 批量启停：逐条调用，失败不中断整批，最后汇总成功数。
async function batchToggle(enabled: boolean, targets: NVIDIAKey[]): Promise<void> {
  if (batchBusy.value || targets.length === 0) return
  batchBusy.value = true
  let ok = 0
  for (const key of targets) {
    try {
      await nvidiaKeysApi.setEnabled(key.id, enabled)
      ok += 1
    } catch (error) {
      if (isDisposed()) break
      toastError(errorMessage(error, `更新 Key ${key.masked} 状态失败。`))
    }
  }
  await loadKeys()
  if (!isDisposed()) {
    toastSuccess(`已${enabled ? '启用' : '停用'} ${ok}/${targets.length} 个 Key。`)
    batchBusy.value = false
  }
}

async function confirmBatchDelete(): Promise<void> {
  const targets = pendingBatchDelete.value
  if (!targets || targets.length === 0 || deleting.value) return
  deleting.value = true
  let ok = 0
  for (const key of targets) {
    try {
      await nvidiaKeysApi.remove(key.id)
      ok += 1
    } catch (error) {
      if (isDisposed()) break
      toastError(errorMessage(error, `删除 Key ${key.masked} 失败。`))
    }
  }
  pendingBatchDelete.value = null
  await loadKeys()
  if (!isDisposed()) {
    toastSuccess(`已删除 ${ok}/${targets.length} 个 Key。`)
    deleting.value = false
  }
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="NVIDIA Key"
        subtitle="管理上游凭据状态。页面只显示脱敏值，不保留 Key 明文。"
      >
        <template #actions>
          <UiButton
            data-testid="open-batch-import"
            variant="secondary"
            icon="upload"
            @click="batchOpen = true"
          >
            批量导入
          </UiButton>
          <UiButton
            data-testid="test-all-keys"
            variant="primary"
            icon="play"
            :loading="testingAll"
            loading-label="测活中…"
            :disabled="loading"
            @click="testAll"
          >
            顺序测活全部
          </UiButton>
        </template>
      </UiPageHeader>

      <!-- Single import -->
      <UiCard
        title="单个导入"
        subtitle="粘贴 Key 后回车即提交，提交后立即清空输入框。"
      >
        <form
          data-testid="single-import-form"
          class="flex flex-col gap-3 sm:flex-row sm:items-start"
          @submit.prevent="importOne"
        >
          <UiField
            class="min-w-0 flex-1"
            :error="importError"
          >
            <input
              id="nvidia-key-input"
              v-model="singleKey"
              class="input-field font-mono-data"
              name="nvidia-key"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="粘贴 NVIDIA Key，提交后立即清空"
              aria-label="NVIDIA Key"
            >
          </UiField>
          <UiButton
            variant="primary"
            type="submit"
            :loading="submitting"
            loading-label="导入中…"
          >
            导入
          </UiButton>
        </form>
        <Transition name="fade">
          <div
            v-if="singleResult"
            class="mt-3"
          >
            <UiBadge
              :variant="singleResult.status === 'imported' ? 'success' : 'warning'"
              :label="`行 ${singleResult.line ?? 1} · ${singleResult.masked || '—'} · ${singleResult.status}${singleResult.reason ? ` · ${singleResult.reason}` : ''}`"
              :dot="false"
            />
          </div>
        </Transition>
      </UiCard>

      <!-- Mobile hint -->
      <p
        data-testid="mobile-batch-hint"
        class="panel-inset mt-4 px-3 py-2 text-xs text-[var(--color-text-muted)] md:hidden"
      >
        移动端支持逐条启停、单测和删除；批量导入等高级操作请在桌面端或页面右上角完成。
      </p>

      <!-- Key list -->
      <div class="mt-5">
        <UiStatePanel
          :loading="loading"
          :error="loadError"
          :empty="keyList.length === 0"
          loadingLabel="NVIDIA Key 加载中…"
          skeleton="table"
          emptyLabel="尚未导入 NVIDIA Key"
          emptyHint="通过上方单个导入或右上角批量导入添加第一个上游凭据。"
          empty-icon="key"
          errorTestId="nvidia-keys-load-error"
          retryTestId="nvidia-keys-retry"
          @retry="loadKeys"
        >
          <KeyTable
            :keys="keyList"
            :busy-id="busyId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="pendingDelete = $event"
            @batch-toggle="batchToggle"
            @batch-remove="pendingBatchDelete = $event"
          />
          <KeyCards
            :keys="keyList"
            :busy-id="busyId"
            @toggle="toggleKey"
            @test="testKey"
            @remove="pendingDelete = $event"
          />
        </UiStatePanel>
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
      :masked-by-id="maskedById"
      @close="testDialogOpen = false"
    />
    <UiConfirmDialog
      :open="pendingDelete !== null"
      title="删除 NVIDIA Key"
      :message="pendingDelete ? `将永久删除 ${pendingDelete.masked}（#${pendingDelete.id}）。删除后该凭据立即退出轮询池，正在排队的请求会切换到其他可用 Key。` : ''"
      confirm-label="删除"
      :busy="deleting"
      confirm-test-id="confirm-delete-key"
      cancel-test-id="cancel-delete-key"
      @confirm="confirmDelete"
      @cancel="pendingDelete = null"
    />
    <UiConfirmDialog
      :open="pendingBatchDelete !== null"
      title="批量删除 NVIDIA Key"
      :message="pendingBatchDelete ? `将永久删除选中的 ${pendingBatchDelete.length} 个 Key。删除后这些凭据立即退出轮询池，正在排队的请求会切换到其他可用 Key。` : ''"
      confirm-label="全部删除"
      :busy="deleting || batchBusy"
      @confirm="confirmBatchDelete"
      @cancel="pendingBatchDelete = null"
    />
  </div>
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
</style>
