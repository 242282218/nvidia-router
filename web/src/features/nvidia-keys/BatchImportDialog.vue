<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { ApiError, isRecord } from '../../shared/api/client'
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
  <Transition name="modal">
    <div
      v-if="open"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      @click.self="close"
      @keydown.esc="close"
    >
      <section class="modal-panel flex max-h-[calc(100vh-2rem)] flex-col overflow-hidden animate-scale-in">
        <!-- Header -->
        <div class="flex shrink-0 items-center justify-between gap-4 border-b border-[var(--color-border)] px-4 py-4 sm:px-6">
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-[var(--color-text)]">
              批量导入 NVIDIA Key
            </h2>
            <p class="mt-0.5 truncate text-sm text-[var(--color-text-muted)]">
              每行一个 Key。导入只做本地校验和保存，不执行上游测活。
            </p>
          </div>
          <button
            class="btn-secondary inline-flex min-h-10 shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-sm"
            type="button"
            aria-label="关闭批量导入窗口"
            @click="close"
          >
            <svg
              class="h-4 w-4"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
            <span>关闭</span>
          </button>
        </div>

        <!-- Body -->
        <div class="min-h-0 overflow-y-auto p-4 sm:p-6">
          <form
            class="space-y-4"
            @submit.prevent="submit"
          >
            <p class="rounded-lg border border-[#818CF8]/20 bg-[#818CF8]/5 px-3 py-2 text-xs text-[var(--color-info)]">
              导入完成后，如需验证上游可用性，请返回页面点击“顺序测活全部”。
            </p>
            <textarea
              v-model="text"
              class="input-field min-h-[140px] w-full font-mono text-sm"
              name="batch-keys"
              autocomplete="off"
              spellcheck="false"
              placeholder="每行一个 NVIDIA Key"
            />
            <Transition name="fade">
              <p
                v-if="errorMessage"
                class="text-sm text-[#F87171]"
                role="alert"
              >
                {{ errorMessage }}
              </p>
            </Transition>
            <div class="flex justify-end gap-2">
              <button
                class="btn-secondary rounded-lg px-4 py-2 text-sm"
                type="button"
                @click="close"
              >
                取消
              </button>
              <button
                class="btn-primary rounded-lg px-4 py-2 text-sm"
                type="submit"
                :disabled="submitting"
              >
                {{ submitting ? '导入中…' : '导入' }}
              </button>
            </div>
          </form>

          <!-- Results -->
          <Transition name="fade">
            <div
              v-if="results.length"
              data-testid="batch-import-results"
              class="mt-6 overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-sunken)]/40"
            >
              <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
                <div>
                  <h3 class="text-sm font-medium text-[var(--color-text)]">
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
                  <button
                    v-if="resultSummary.imported > 0"
                    class="btn-secondary ml-1 rounded-md px-2.5 py-1 text-xs"
                    type="button"
                    :disabled="testing"
                    data-testid="test-newly-imported"
                    @click="testNewlyImported"
                  >
                    {{ testing ? '测活中…' : `测活新增 ${resultSummary.imported} 个` }}
                  </button>
                </div>
              </div>
              <div
                class="max-h-[min(42vh,360px)] overflow-auto focus-within:ring-2 focus-within:ring-[var(--color-focus)]/40"
                tabindex="0"
                aria-label="批量导入结果，可横向滚动"
              >
                <table class="data-table min-w-[560px]">
                  <thead class="sticky top-0 z-10">
                    <tr>
                      <th class="data-table-th w-16">
                        行
                      </th>
                      <th class="data-table-th">
                        Key
                      </th>
                      <th class="data-table-th w-32">
                        状态
                      </th>
                      <th class="data-table-th">
                        原因
                      </th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-[var(--color-border)]">
                    <tr
                      v-for="result in results"
                      :key="`${result.line}-${result.masked}`"
                      class="transition-colors hover:bg-[var(--color-hover)]"
                    >
                      <td class="data-table-td">
                        {{ result.line ?? '—' }}
                      </td>
                      <td
                        class="data-table-td max-w-[220px] truncate font-mono text-[var(--color-info)]"
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
                    <code class="truncate font-mono text-[var(--color-info)]">#{{ result.id }}</code>
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
        </div>

        <div class="flex shrink-0 justify-end border-t border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3 sm:px-6">
          <button
            class="btn-secondary min-h-10 rounded-lg px-4 py-2 text-sm"
            type="button"
            @click="close"
          >
            关闭
          </button>
        </div>
      </section>
    </div>
  </Transition>
</template>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: all 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from .modal-panel,
.modal-leave-to .modal-panel {
  transform: scale(0.95);
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>