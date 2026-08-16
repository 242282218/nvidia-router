<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
import { formatDate } from '../../shared/format'
import PageHeader from '../../shared/components/PageHeader.vue'
import LoadingSpinner from '../../shared/components/LoadingSpinner.vue'
import { auditApi } from './api'
import { AUDIT_ACTIONS, type AuditEntry } from './types'

const items = ref<AuditEntry[]>([])
const total = ref(0)
const loading = ref(false)
const errorMessage = ref('')
const page = ref(1)
const pageSize = 50
const hasMore = ref(false)
const selectedAction = ref('')
let loadSequence = 0
let disposed = false
let hasLoaded = false

onMounted(() => {
  void load()
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
})

async function load(): Promise<void> {
  if (disposed) return
  const sequence = ++loadSequence
  loading.value = true
  try {
    const response: unknown = await auditApi.list({ page: page.value, pageSize, action: selectedAction.value || undefined })
    if (disposed || sequence !== loadSequence) return
    const data = isAuditPage(response)
    items.value = data.items
    total.value = data.total
    hasMore.value = data.has_more === true
    errorMessage.value = ''
    hasLoaded = true
  } catch (error) {
    if (disposed || sequence !== loadSequence) return
    errorMessage.value = error instanceof ApiError ? error.message : '审计日志加载失败。'
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

function isAuditPage(value: unknown): { items: AuditEntry[]; total: number; has_more: boolean } {
  if (!isRecord(value) || !isRecord((value as { data?: unknown }).data)) {
    throw new TypeError('Invalid audit log response.')
  }
  const data = (value as { data: Record<string, unknown> }).data
  if (!Array.isArray(data.items) || !isFiniteNumber(data.total)) {
    throw new TypeError('Invalid audit log page payload.')
  }
  const items = data.items.filter(isAuditEntry).map((entry) => entry)
  return { items, total: data.total, has_more: data.has_more === true }
}

function isAuditEntry(value: unknown): value is AuditEntry {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.action === 'string'
    && typeof value.created_at === 'string'
}

function applyFilter(): void {
  page.value = 1
  void load()
}

function nextPage(): void {
  if (!hasMore.value || loading.value) return
  page.value += 1
  void load()
}

function prevPage(): void {
  if (page.value <= 1 || loading.value) return
  page.value -= 1
  void load()
}

function parseDetail(raw: string | undefined): string {
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw))
  } catch {
    return raw
  }
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="安全管理"
        title="审计日志"
        subtitle="记录所有管理操作与认证事件，用于事后追溯。"
      >
        <template #actions>
          <button
            class="btn-ghost"
            type="button"
            :disabled="loading"
            @click="load"
          >
            {{ loading ? '刷新中…' : '刷新' }}
          </button>
        </template>
      </PageHeader>

      <div class="card mt-5 p-4">
        <div class="flex flex-wrap items-center gap-3">
          <label
            class="text-sm text-[var(--color-text-secondary)]"
            for="audit-action-filter"
          >
            操作类型
          </label>
          <select
            id="audit-action-filter"
            v-model="selectedAction"
            class="input-field w-auto"
            data-testid="audit-action-filter"
            @change="applyFilter"
          >
            <option value="">
              全部操作
            </option>
            <option
              v-for="action in AUDIT_ACTIONS"
              :key="action"
              :value="action"
            >
              {{ action }}
            </option>
          </select>
        </div>
      </div>

      <div
        v-if="errorMessage"
        class="card mt-4 flex flex-wrap items-center justify-between gap-3 p-6 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        <span>{{ errorMessage }}</span>
        <button
          class="btn-secondary"
          type="button"
          @click="load"
        >
          重新加载
        </button>
      </div>

      <div
        v-else
        class="card mt-4 overflow-hidden"
        :aria-busy="loading"
      >
        <!-- min-w keeps the table from squeezing on narrow screens; the wrapper
             scrolls horizontally instead (mobile-friendly overflow pattern). -->
        <div class="overflow-x-auto">
          <table class="data-table min-w-[640px]">
            <caption class="sr-only">
              审计日志，当前第 {{ page }} 页
            </caption>
            <thead>
              <tr>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  时间
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  操作
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  目标
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  来源 IP
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  详情
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="loading && items.length === 0">
                <td
                  class="data-table-td"
                  colspan="5"
                >
                  <div class="flex justify-center py-4">
                    <LoadingSpinner label="审计日志加载中…" />
                  </div>
                </td>
              </tr>
              <tr v-else-if="items.length === 0">
                <td
                  class="data-table-td text-center text-[var(--color-text-muted)]"
                  colspan="5"
                >
                  <template v-if="hasLoaded">
                    暂无审计记录。可调整操作类型或刷新。
                  </template>
                  <template v-else>
                    暂无审计记录
                  </template>
                </td>
              </tr>
              <tr
                v-for="entry in items"
                v-else
                :key="entry.id"
                class="transition-colors hover:bg-[var(--color-hover)]"
              >
                <td class="data-table-td font-mono text-xs">
                  {{ formatDate(entry.created_at, { seconds: true }) }}
                </td>
                <td class="data-table-td">
                  <span class="rounded bg-[color-mix(in_srgb,var(--color-accent)_10%,transparent)] px-2 py-0.5 font-mono text-xs font-medium text-[var(--color-accent-text)]">
                    {{ entry.action }}
                  </span>
                </td>
                <td class="data-table-td text-xs">
                  <template v-if="entry.target_id">
                    {{ entry.target_type }} #{{ entry.target_id }}
                  </template>
                  <template v-else>
                    {{ entry.target_type || '—' }}
                  </template>
                </td>
                <td class="data-table-td font-mono text-xs">
                  {{ entry.client_ip || '—' }}
                </td>
                <td class="data-table-td max-w-[280px]">
                  <span
                    :title="parseDetail(entry.detail) || undefined"
                    class="block truncate font-mono text-xs text-[var(--color-text-subtle)]"
                  >{{ parseDetail(entry.detail) || '—' }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm text-[var(--color-text-secondary)]">
        <span>
          共 {{ total }} 条记录
        </span>
        <div class="flex items-center gap-2">
          <span
            v-if="loading && items.length > 0"
            class="mr-1 flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]"
          >
            <LoadingSpinner
              size="sm"
              label="加载中…"
            />
          </span>
          <button
            class="btn-ghost"
            type="button"
            :disabled="page <= 1 || loading"
            aria-label="上一页"
            data-testid="audit-prev"
            @click="prevPage"
          >
            上一页
          </button>
          <span
            class="font-mono text-xs tabular-nums"
            aria-live="polite"
          >
            第 {{ page }} 页
          </span>
          <button
            class="btn-ghost"
            type="button"
            :disabled="!hasMore || loading"
            aria-label="下一页"
            data-testid="audit-next"
            @click="nextPage"
          >
            下一页
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
