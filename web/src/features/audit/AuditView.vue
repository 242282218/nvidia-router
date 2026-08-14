<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
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

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())}`
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
      <header class="section-header">
        <div>
          <p class="text-xs font-medium uppercase tracking-wider text-[var(--color-warning)]">
            安全管理
          </p>
          <h1 class="page-title mt-1">
            审计日志
          </h1>
          <p class="page-subtitle">
            记录所有管理操作与认证事件，用于事后追溯。
          </p>
        </div>
      </header>

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
            class="input-field rounded-lg px-3 py-2 text-sm"
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
          <button
            class="btn-ghost rounded-lg px-3 py-2 text-sm"
            type="button"
            @click="applyFilter"
          >
            刷新
          </button>
        </div>
      </div>

      <p
        v-if="errorMessage"
        class="mt-4 rounded-lg bg-[var(--color-danger)]/10 p-3 text-sm text-[var(--color-danger)]"
      >
        {{ errorMessage }}
      </p>

      <div class="card mt-4 overflow-hidden">
        <!-- min-w keeps the table from squeezing on narrow screens; the wrapper
             scrolls horizontally instead (mobile-friendly overflow pattern). -->
        <div class="overflow-x-auto">
          <table class="w-full min-w-[640px] text-left text-sm">
            <thead class="border-b border-[var(--color-border)] bg-[var(--color-surface)]/60 text-xs uppercase tracking-wider text-[var(--color-text-subtle)]">
              <tr>
                <th class="px-4 py-3">
                  时间
                </th>
                <th class="px-4 py-3">
                  操作
                </th>
                <th class="px-4 py-3">
                  目标
                </th>
                <th class="px-4 py-3">
                  来源 IP
                </th>
                <th class="px-4 py-3">
                  详情
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="loading && items.length === 0">
                <td
                  class="px-4 py-8 text-center text-[var(--color-text-muted)]"
                  colspan="5"
                >
                  加载中…
                </td>
              </tr>
              <tr v-else-if="!loading && items.length === 0">
                <td
                  class="px-4 py-8 text-center text-[var(--color-text-muted)]"
                  colspan="5"
                >
                  暂无审计记录{{ hasLoaded ? '' : '。' }}
                </td>
              </tr>
              <tr
                v-for="entry in items"
                v-else
                :key="entry.id"
                class="border-b border-[var(--color-border)] last:border-0 hover:bg-[var(--color-surface)]/50"
              >
                <td class="px-4 py-3 font-mono text-xs text-[var(--color-text-secondary)]">
                  {{ formatDate(entry.created_at) }}
                </td>
                <td class="px-4 py-3">
                  <span class="rounded bg-[var(--color-accent)]/10 px-2 py-0.5 text-xs font-medium text-[var(--color-accent)]">
                    {{ entry.action }}
                  </span>
                </td>
                <td class="px-4 py-3 text-xs text-[var(--color-text-secondary)]">
                  <template v-if="entry.target_id">
                    {{ entry.target_type }} #{{ entry.target_id }}
                  </template>
                  <template v-else>
                    {{ entry.target_type || '—' }}
                  </template>
                </td>
                <td class="px-4 py-3 font-mono text-xs text-[var(--color-text-secondary)]">
                  {{ entry.client_ip || '—' }}
                </td>
                <td class="max-w-[280px] truncate px-4 py-3 font-mono text-xs text-[var(--color-text-subtle)]">
                  <span
                    :title="parseDetail(entry.detail) || undefined"
                    class="block truncate"
                  >{{ parseDetail(entry.detail) }}</span>
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
            <span class="h-3 w-3 animate-spin rounded-full border-2 border-[var(--color-border-strong)] border-t-[var(--color-accent)]" />
            加载中…
          </span>
          <button
            class="btn-ghost rounded-lg px-3 py-1.5 disabled:opacity-40"
            type="button"
            :disabled="page <= 1 || loading"
            data-testid="audit-prev"
            @click="prevPage"
          >
            上一页
          </button>
          <span class="font-mono text-xs">
            第 {{ page }} 页
          </span>
          <button
            class="btn-ghost rounded-lg px-3 py-1.5 disabled:opacity-40"
            type="button"
            :disabled="!hasMore || loading"
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
