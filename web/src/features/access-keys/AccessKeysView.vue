<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastSuccess } from '../../shared/toast'
import { accessKeysApi } from './api'
import CreateAccessKeyDialog from './CreateAccessKeyDialog.vue'
import EditAccessKeyPolicyDialog from './EditAccessKeyPolicyDialog.vue'
import type { AccessKey } from './types'

const keys = ref<AccessKey[]>([])
const loading = ref(false)
const loadError = ref('')
const dialogOpen = ref(false)
const editDialogOpen = ref(false)
const editingKey = ref<AccessKey | null>(null)
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
    const response: unknown = await accessKeysApi.list()
    if (disposed || sequence !== loadSequence) return
    if (!isDataArrayResponse(response, isAccessKey)) {
      throw new TypeError('Invalid Access Key list response.')
    }
    keys.value = response.data
  } catch (error) {
    if (disposed || sequence !== loadSequence) return
    // A failed load must not read as "no keys exist": keep the list untouched
    // and surface a persistent error with a retry instead of an empty state.
    loadError.value = errorMessage(error, 'Access Key 列表加载失败。')
    toastError(loadError.value)
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

function isAccessKey(value: unknown): value is AccessKey {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.name === 'string'
    && typeof value.key_prefix === 'string'
    && typeof value.created_at === 'string'
    && isOptionalString(value.last_used_at)
    && isOptionalString(value.revoked_at)
    && isOptionalString(value.expires_at)
    && isFiniteNumber(value.rpm_limit)
    && isFiniteNumber(value.tpm_limit)
    && isFiniteNumber(value.max_concurrent)
}

function isOptionalString(value: unknown): boolean {
  return value === undefined || typeof value === 'string'
}

function openEditPolicy(key: AccessKey): void {
  editingKey.value = key
  editDialogOpen.value = true
}

async function revokeKey(key: AccessKey): Promise<void> {
  if (busyId.value === key.id) return
  // Two-step destructive confirmation, matching the NVIDIA Key page instead of
  // the native window.confirm (which is visually detached from the app chrome).
  if (confirmingId.value === key.id) {
    confirmingId.value = null
    busyId.value = key.id
    try {
      await accessKeysApi.revoke(key.id)
      if (disposed) return
      await loadKeys()
      toastSuccess(`Access Key「${key.name}」已撤销。`)
    } catch (error) {
      if (disposed) return
      toastError(errorMessage(error, 'Access Key 撤销失败。'))
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

function formatDate(value?: string): string {
  if (!value) return '从未使用'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}

function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(value)
}

// budgetUsagePercent returns the fraction of a key's token budget already
// consumed (0..100). Unlimited keys (budget 0) show no meter.
function budgetUsagePercent(key: AccessKey): number {
  if (key.token_budget <= 0) return 0
  return Math.min(100, Math.round((key.consumed_tokens / key.token_budget) * 100))
}

// isExpired reports whether the key's expiry time has passed.
function isExpired(key: AccessKey): boolean {
  if (!key.expires_at) return false
  const expiry = new Date(key.expires_at)
  return !Number.isNaN(expiry.getTime()) && expiry.getTime() <= Date.now()
}

// isBudgetExhausted reports whether a budgeted key has consumed its whole
// token budget.
function isBudgetExhausted(key: AccessKey): boolean {
  return key.token_budget > 0 && key.consumed_tokens >= key.token_budget
}

// keyState derives the operator-facing state with a fixed precedence:
// revoked > expired > budget exhausted > valid. A key can be simultaneously
// expired and out of budget; the first condition wins so the UI never claims
// a refused key is usable.
function keyState(key: AccessKey): { label: string; badge: string } {
  if (key.revoked_at) return { label: '已撤销', badge: 'badge-muted' }
  if (isExpired(key)) return { label: '已过期', badge: 'badge-warning' }
  if (isBudgetExhausted(key)) return { label: '预算已耗尽', badge: 'badge-danger' }
  return { label: '有效', badge: 'badge-success' }
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
            Access Key
          </h1>
          <p class="page-subtitle">
            管理调用路由器的下游设备和客户端凭证。
          </p>
        </div>
        <button
          data-testid="open-create-access-key"
          class="btn-primary rounded-lg px-4 py-2 text-sm"
          type="button"
          @click="dialogOpen = true"
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
                d="M12 4.5v15m7.5-7.5h-15"
              />
            </svg>
            创建 Access Key
          </span>
        </button>
      </header>

      <div class="overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div
          v-if="loadError"
          data-testid="access-keys-load-error"
          class="flex flex-wrap items-center justify-between gap-3 p-6 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          <span>{{ loadError }}</span>
          <button
            data-testid="access-keys-retry"
            class="btn-secondary rounded-lg px-3 py-1.5 text-sm"
            type="button"
            :disabled="loading"
            @click="loadKeys"
          >
            {{ loading ? '重试中…' : '重新加载' }}
          </button>
        </div>
        <template v-else-if="loading">
          <div class="flex items-center gap-3 p-6 text-sm text-[var(--color-text-muted)]">
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
        </template>
        <template v-else-if="keys.length === 0">
          <div class="p-6 text-sm text-[var(--color-text-muted)]">
            尚未创建 Access Key。
          </div>
        </template>
        <template v-else>
          <!-- Mobile cards -->
          <div
            data-testid="access-key-cards"
            class="space-y-2 p-4 md:hidden"
          >
            <article
              v-for="key in keys"
              :key="`card-${key.id}`"
              class="card-hover p-4 animate-slide-up"
            >
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <h3 class="text-sm font-medium text-[var(--color-text)]">
                    {{ key.name }}
                  </h3>
                  <code class="mt-1 block truncate font-mono text-xs text-[var(--color-info)]">{{ key.key_prefix }}</code>
                </div>
                <span
                  :class="keyState(key).badge"
                  class="shrink-0"
                >{{ keyState(key).label }}</span>
              </div>
              <div class="mt-3 space-y-1 text-xs text-[var(--color-text-muted)]">
                <div class="flex justify-between">
                  <span>创建时间</span>
                  <span>{{ formatDate(key.created_at) }}</span>
                </div>
                <div class="flex justify-between">
                  <span>最后使用</span>
                  <span>{{ formatDate(key.last_used_at) }}</span>
                </div>
                <div
                  v-if="key.expires_at"
                  class="flex justify-between"
                >
                  <span>过期时间</span>
                  <span>{{ formatDate(key.expires_at) }}</span>
                </div>
                <div
                  v-if="key.token_budget > 0"
                  class="flex justify-between"
                >
                  <span>Token 预算</span>
                  <span>{{ formatTokens(key.consumed_tokens) }} / {{ formatTokens(key.token_budget) }}（{{ budgetUsagePercent(key) }}%）</span>
                </div>
              </div>
              <div class="mt-4 flex gap-2">
                <button
                  :data-testid="`mobile-edit-access-key-policy-${key.id}`"
                  class="btn-secondary flex-1 rounded-lg py-2 text-sm"
                  type="button"
                  :disabled="Boolean(key.revoked_at)"
                  @click="openEditPolicy(key)"
                >
                  编辑策略
                </button>
                <button
                  :data-testid="`mobile-revoke-access-key-${key.id}`"
                  class="btn-danger flex-1 rounded-lg py-2 text-sm"
                  type="button"
                  :disabled="Boolean(key.revoked_at) || busyId === key.id"
                  @click="revokeKey(key)"
                >
                  {{ confirmingId === key.id ? '确认撤销？' : '撤销' }}
                </button>
              </div>
            </article>
          </div>

          <!-- Desktop table -->
          <table
            data-testid="access-key-table"
            class="hidden min-w-full text-left text-sm md:table"
          >
            <thead>
              <tr>
                <th class="data-table-th">
                  名称
                </th>
                <th class="data-table-th">
                  前缀
                </th>
                <th class="data-table-th">
                  创建时间
                </th>
                <th class="data-table-th">
                  最后使用
                </th>
                <th class="data-table-th">
                  Token 预算
                </th>
                <th class="data-table-th">
                  状态
                </th>
                <th class="data-table-th text-right">
                  操作
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-[var(--color-border)]">
              <tr
                v-for="key in keys"
                :key="key.id"
                class="transition-colors hover:bg-[color-mix(in_srgb,var(--color-border)_30%,transparent)]"
              >
                <td class="data-table-td font-medium text-[var(--color-text)]">
                  {{ key.name }}
                </td>
                <td class="data-table-td font-mono text-[var(--color-info)]">
                  {{ key.key_prefix }}
                </td>
                <td class="data-table-td text-[var(--color-text-secondary)]">
                  {{ formatDate(key.created_at) }}
                </td>
                <td class="data-table-td text-[var(--color-text-secondary)]">
                  {{ formatDate(key.last_used_at) }}
                </td>
                <td class="data-table-td">
                  <div
                    v-if="key.token_budget > 0"
                    class="w-32"
                    :data-testid="`access-key-budget-${key.id}`"
                  >
                    <div class="flex justify-between font-mono text-xs text-[var(--color-text-muted)]">
                      <span>{{ formatTokens(key.consumed_tokens) }} / {{ formatTokens(key.token_budget) }}</span>
                      <span>{{ budgetUsagePercent(key) }}%</span>
                    </div>
                    <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-border)]">
                      <div
                        class="h-full rounded-full transition-all"
                        :class="budgetUsagePercent(key) >= 90 ? 'bg-[var(--color-danger)]' : budgetUsagePercent(key) >= 60 ? 'bg-[var(--color-warning)]' : 'bg-[var(--color-success)]'"
                        :style="{ width: `${budgetUsagePercent(key)}%` }"
                      />
                    </div>
                  </div>
                  <span
                    v-else
                    class="text-xs text-[var(--color-text-subtle)]"
                  >
                    不限
                  </span>
                </td>
                <td class="data-table-td">
                  <span
                    :class="keyState(key).badge"
                  >{{ keyState(key).label }}</span>
                  <span
                    v-if="key.expires_at && keyState(key).label !== '已过期'"
                    class="ml-2 block text-xs text-[var(--color-text-subtle)]"
                  >
                    {{ formatDate(key.expires_at) }} 过期
                  </span>
                </td>
                <td class="data-table-td text-right">
                  <button
                    :data-testid="`edit-access-key-policy-${key.id}`"
                    class="btn-secondary rounded-md px-3 py-1 text-xs"
                    type="button"
                    :disabled="Boolean(key.revoked_at)"
                    @click="openEditPolicy(key)"
                  >
                    编辑策略
                  </button>
                  <button
                    :data-testid="`revoke-access-key-${key.id}`"
                    class="btn-danger ml-2 rounded-md px-3 py-1 text-xs"
                    type="button"
                    :disabled="Boolean(key.revoked_at) || busyId === key.id"
                    @click="revokeKey(key)"
                  >
                    {{ confirmingId === key.id ? '确认撤销？' : '撤销' }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </template>
      </div>
    </div>

    <CreateAccessKeyDialog
      :open="dialogOpen"
      @close="dialogOpen = false"
      @created="loadKeys"
    />
    <EditAccessKeyPolicyDialog
      :open="editDialogOpen"
      :access-key="editingKey"
      @close="editDialogOpen = false"
      @saved="loadKeys"
    />
  </div>
</template>
