<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
import { formatDate } from '../../shared/format'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiSelect from '../../shared/ui/UiSelect.vue'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import { auditApi } from './api'
import { AUDIT_ACTIONS, type AuditEntry } from './types'

withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })

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

// 操作语义 → 时间线节点色：删除/撤销 danger、创建/导入 success、认证 info、其余 accent。
function actionTone(action: string): 'danger' | 'success' | 'info' | 'accent' {
  if (action.includes('delete') || action.includes('revoke')) return 'danger'
  if (action.includes('create') || action.includes('import')) return 'success'
  if (action.includes('login') || action.includes('logout') || action.includes('password')) return 'info'
  return 'accent'
}

const toneDotClass: Record<string, string> = {
  danger: 'border-[var(--color-danger)]',
  success: 'border-[var(--color-success)]',
  info: 'border-[var(--color-info)]',
  accent: 'border-[var(--color-accent)]',
}
</script>

<template>
  <div :class="embedded ? '' : 'page-container'">
    <div :class="embedded ? '' : 'content-wrapper'">
      <UiPageHeader
        v-if="!embedded"
        eyebrow="系统观测"
        title="审计日志"
        subtitle="记录所有管理操作与认证事件，用于事后追溯。"
      >
        <template #actions>
          <UiButton
            variant="ghost"
            :loading="loading"
            loading-label="刷新中…"
            icon="refresh"
            @click="load"
          >
            刷新
          </UiButton>
        </template>
      </UiPageHeader>

      <!-- Embedded toolbar -->
      <div
        v-if="embedded"
        class="mb-3 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border-subtle)] pb-3"
      >
        <div class="flex items-center gap-2">
          <label
            class="text-xs text-[var(--color-text-muted)]"
            for="audit-action-filter-embedded"
          >
            筛选操作
          </label>
          <UiSelect
            id="audit-action-filter-embedded"
            v-model="selectedAction"
            class="w-44"
            aria-label="筛选操作类型"
            @change="applyFilter"
          >
            <option value="">
              全部操作类型
            </option>
            <option
              v-for="action in AUDIT_ACTIONS"
              :key="action"
              :value="action"
            >
              {{ action }}
            </option>
          </UiSelect>
        </div>
        <UiButton
          variant="ghost"
          size="sm"
          :loading="loading"
          loading-label="刷新中…"
          icon="refresh"
          @click="load"
        >
          刷新
        </UiButton>
      </div>

      <div
        v-if="!embedded"
        class="card mt-5 p-4"
      >
        <div class="flex flex-wrap items-center gap-3">
          <label
            class="text-sm text-[var(--color-text-secondary)]"
            for="audit-action-filter"
          >
            操作类型
          </label>
          <UiSelect
            id="audit-action-filter"
            v-model="selectedAction"
            class="w-52"
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
          </UiSelect>
        </div>
      </div>

      <div
        v-if="errorMessage"
        class="card mt-4 flex flex-wrap items-center justify-between gap-3 p-5 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        <span>{{ errorMessage }}</span>
        <UiButton
          variant="secondary"
          size="sm"
          icon="refresh"
          @click="load"
        >
          重新加载
        </UiButton>
      </div>

      <div
        v-else
        class="card mt-4 overflow-hidden"
        :aria-busy="loading"
      >
        <UiSkeleton
          v-if="loading && items.length === 0"
          variant="table"
          :lines="6"
        />
        <!-- 竖向时间线：管理操作按时间自上而下，节点色承载操作语义 -->
        <div
          v-else
          class="p-5"
        >
          <p
            v-if="items.length === 0"
            class="py-8 text-center text-sm text-[var(--color-text-muted)]"
          >
            <template v-if="hasLoaded">
              暂无审计记录。可调整操作类型或刷新。
            </template>
            <template v-else>
              暂无审计记录
            </template>
          </p>
          <ol
            v-else
            class="relative space-y-5"
            :aria-label="`审计日志，共 ${total} 条`"
          >
            <li
              v-for="(entry, index) in items"
              :key="entry.id"
              class="relative flex gap-4 pl-7"
            >
              <!-- 连接线：最后一项不画 -->
              <span
                v-if="index < items.length - 1"
                class="absolute left-[7px] top-4 bottom-[-20px] w-px bg-[var(--color-border)]"
                aria-hidden="true"
              />
              <span
                class="absolute left-0 top-1.5 h-3.5 w-3.5 rounded-full border-2 bg-[var(--color-surface)]"
                :class="toneDotClass[actionTone(entry.action)]"
                aria-hidden="true"
              />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span class="rounded bg-[color-mix(in_srgb,var(--color-accent)_10%,transparent)] px-2 py-0.5 font-mono-data text-xs font-medium text-[var(--color-accent-text)]">
                    {{ entry.action }}
                  </span>
                  <span class="font-mono-data text-xs text-[var(--color-text-muted)]">
                    {{ formatDate(entry.created_at, { seconds: true }) }}
                  </span>
                  <span
                    v-if="entry.client_ip"
                    class="font-mono-data text-xs text-[var(--color-text-subtle)]"
                  >
                    {{ entry.client_ip }}
                  </span>
                </div>
                <p class="mt-1 text-xs text-[var(--color-text-secondary)]">
                  <template v-if="entry.target_id">
                    {{ entry.target_type }} #{{ entry.target_id }}
                  </template>
                  <template v-else>
                    {{ entry.target_type || '—' }}
                  </template>
                  <span
                    v-if="parseDetail(entry.detail)"
                    class="mt-0.5 block truncate font-mono-data text-[var(--color-text-subtle)] sm:ml-2 sm:mt-0 sm:inline-block sm:max-w-[280px] sm:align-bottom"
                    :title="parseDetail(entry.detail)"
                  >{{ parseDetail(entry.detail) }}</span>
                </p>
              </div>
            </li>
          </ol>
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
            role="status"
          >
            <span
              class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--color-info)]"
              aria-hidden="true"
            />加载中…
          </span>
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="page <= 1 || loading"
            aria-label="上一页"
            data-testid="audit-prev"
            @click="prevPage"
          >
            上一页
          </UiButton>
          <span
            class="font-mono-data text-xs"
            aria-live="polite"
          >
            第 {{ page }} 页
          </span>
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="!hasMore || loading"
            aria-label="下一页"
            data-testid="audit-next"
            @click="nextPage"
          >
            下一页
          </UiButton>
        </div>
      </div>
    </div>
  </div>
</template>
