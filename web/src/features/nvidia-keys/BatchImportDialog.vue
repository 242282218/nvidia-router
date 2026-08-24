<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { ApiError, isRecord } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import UiTextarea from '../../shared/ui/UiTextarea.vue'
import { nvidiaKeysApi } from './api'
import { isImportResult } from './types'
import type { ImportResult, KeyTestResult } from './types'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; imported: [] }>()
const text = ref('')
const results = ref<ImportResult[]>([])
const errorMessage = ref('')
const submitting = ref(false)
const testing = ref(false)
const testResults = ref<KeyTestResult[]>([])

watch(() => props.open, (open) => {
  if (!open) {
    text.value = ''
    results.value = []
    errorMessage.value = ''
    testResults.value = []
    testing.value = false
  }
})

const resultSummary = computed(() => {
  const imported = results.value.filter((result) => result.status === 'imported').length
  const failed = results.value.length - imported
  return { imported, failed }
})

function close(): void {
  emit('close')
}

function statusClass(status: string): string {
  switch (status) {
    case 'imported': return 'badge-success'
    case 'duplicate': return 'badge-info'
    case 'invalid': return 'badge-danger'
    default: return 'badge-warning'
  }
}

async function submit(): Promise<void> {
  const keys = text.value
  text.value = ''
  results.value = []
  testResults.value = []
  errorMessage.value = ''
  if (!keys.trim()) {
    errorMessage.value = '请先输入至少一行 NVIDIA Key。'
    return
  }
  submitting.value = true
  try {
    const response: unknown = await nvidiaKeysApi.importBatch(keys)
    const rows = isRecord(response) ? response.data : undefined
    if (!Array.isArray(rows) || !rows.every(isImportResult)) {
      throw new TypeError('Invalid batch import response.')
    }
    results.value = rows
    emit('imported')
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '批量导入失败，请稍后重试。'
  } finally {
    submitting.value = false
  }
}

// newlyImported returns the IDs of keys that were actually saved this batch, so
// the "test new keys" action only probes what the import added rather than
// re-running the whole pool (which the list view already exposes).
function newlyImportedIds(): number[] {
  return results.value
    .filter((result) => result.status === 'imported' && result.key?.id)
    .map((result) => result.key!.id)
}

async function testNewlyImported(): Promise<void> {
  const ids = newlyImportedIds()
  if (ids.length === 0 || testing.value) return
  testing.value = true
  errorMessage.value = ''
  testResults.value = []
  try {
    // Sequential probing, same as the list view's "test all": avoids hammering
    // the upstream validator with parallel model fetches for a fresh batch.
    const outcomes: KeyTestResult[] = []
    for (const id of ids) {
      outcomes.push(await nvidiaKeysApi.test(id))
    }
    testResults.value = outcomes
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '新增 Key 测活失败，请稍后重试。'
  } finally {
    testing.value = false
  }
}

function testStatusClass(status: string): string {
  return status === 'valid' ? 'badge-success' : 'badge-danger'
}
</script>

<template>
  <UiModal
    :open="open"
    title="批量导入 NVIDIA Key"
    subtitle="每行一个 Key。导入只做本地校验和保存，不执行上游测活。"
    size="lg"
    @close="close"
  >
    <form
      class="space-y-4"
      @submit.prevent="submit"
    >
      <p class="rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-info)_20%,transparent)] bg-[color-mix(in_srgb,var(--color-info)_5%,transparent)] px-3 py-2 text-xs text-[var(--color-info)]">
        导入完成后，如需验证上游可用性，请返回页面点击“顺序测活全部”。
      </p>
      <UiTextarea
        v-model="text"
        mono
        :rows="7"
        name="batch-keys"
        autocomplete="off"
        spellcheck="false"
        placeholder="每行一个 NVIDIA Key"
        aria-label="批量 NVIDIA Key"
      />
      <p
        v-if="errorMessage"
        class="text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ errorMessage }}
      </p>
      <div class="flex justify-end gap-2">
        <UiButton
          variant="ghost"
          @click="close"
        >
          取消
        </UiButton>
        <UiButton
          variant="primary"
          type="submit"
          :loading="submitting"
          loading-label="导入中…"
        >
          导入
        </UiButton>
      </div>
    </form>

    <!-- Results -->
    <Transition name="fade">
      <div
        v-if="results.length"
        data-testid="batch-import-results"
        class="card mt-5 overflow-hidden"
      >
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
          <div>
            <h3 class="type-heading">
              导入结果
            </h3>
            <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
              共 {{ results.length }} 条记录
            </p>
          </div>
          <div class="flex items-center gap-2 text-xs">
            <span class="badge-success">成功 {{ resultSummary.imported }}</span>
            <span
              v-if="resultSummary.failed"
              class="badge-warning"
            >需处理 {{ resultSummary.failed }}</span>
            <UiButton
              v-if="resultSummary.imported > 0"
              variant="secondary"
              size="sm"
              :loading="testing"
              loading-label="测活中…"
              data-testid="test-newly-imported"
              @click="testNewlyImported"
            >
              测活新增 {{ resultSummary.imported }} 个
            </UiButton>
          </div>
        </div>
        <div
          class="max-h-[min(42vh,360px)] overflow-auto focus-within:outline-2 focus-within:-outline-offset-2 focus-within:outline-[var(--color-focus)]"
          tabindex="0"
          aria-label="批量导入结果，可横向滚动"
        >
          <table class="data-table min-w-[560px]">
            <caption class="sr-only">
              批量导入结果，共 {{ results.length }} 条
            </caption>
            <thead class="sticky top-0 z-[var(--z-sticky)] bg-[var(--color-surface)]">
              <tr>
                <th
                  class="data-table-th w-16"
                  scope="col"
                >
                  行
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  Key
                </th>
                <th
                  class="data-table-th w-32"
                  scope="col"
                >
                  状态
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  原因
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="result in results"
                :key="`${result.line}-${result.masked}`"
                class="data-table-row"
              >
                <td class="data-table-td">
                  {{ result.line ?? '—' }}
                </td>
                <td
                  class="data-table-td max-w-[220px] truncate font-mono-data text-[var(--color-info)]"
                  :title="result.masked || '—'"
                >
                  {{ result.masked || '—' }}
                </td>
                <td class="data-table-td">
                  <span :class="statusClass(result.status)">{{ result.status }}</span>
                </td>
                <td
                  class="data-table-td max-w-[240px] truncate text-[var(--color-text-muted)]"
                  :title="result.reason || '—'"
                >
                  {{ result.reason || '—' }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Probe outcomes for newly imported keys -->
        <div
          v-if="testResults.length"
          data-testid="batch-import-test-results"
          class="border-t border-[var(--color-border)] px-4 py-3"
        >
          <h4 class="text-xs font-medium text-[var(--color-text-muted)]">
            测活结果
          </h4>
          <ul class="mt-2 space-y-1">
            <li
              v-for="result in testResults"
              :key="result.id"
              class="flex items-center justify-between gap-2 text-xs"
            >
              <code class="truncate font-mono-data text-[var(--color-info)]">#{{ result.id }}</code>
              <span :class="'px-2 py-0.5 rounded ' + testStatusClass(result.status)">
                {{ result.status }}
              </span>
              <span
                v-if="result.reason"
                class="truncate text-[var(--color-text-muted)]"
                :title="result.reason"
              >{{ result.reason }}</span>
            </li>
          </ul>
        </div>
      </div>
    </Transition>
  </UiModal>
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
