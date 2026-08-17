<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import { toastError, toastSuccess } from '../../shared/toast'
import { accessKeysApi } from './api'
import AccessKeyCards from './AccessKeyCards.vue'
import AccessKeyTable from './AccessKeyTable.vue'
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
const confirmingRevokeId = ref<number | null>(null)
const confirmingDeleteId = ref<number | null>(null)
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
  if (confirmingRevokeId.value === key.id) {
    confirmingRevokeId.value = null
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
  confirmingRevokeId.value = key.id
  globalThis.setTimeout(() => {
    if (confirmingRevokeId.value === key.id) confirmingRevokeId.value = null
  }, 3000)
}

async function deleteKey(key: AccessKey): Promise<void> {
  if (busyId.value === key.id) return
  if (confirmingDeleteId.value === key.id) {
    confirmingDeleteId.value = null
    busyId.value = key.id
    try {
      await accessKeysApi.delete(key.id)
      if (disposed) return
      keys.value = keys.value.filter((item) => item.id !== key.id)
      toastSuccess(`Access Key「${key.name}」已删除。`)
    } catch (error) {
      if (disposed) return
      toastError(errorMessage(error, 'Access Key 删除失败。'))
    } finally {
      if (!disposed) busyId.value = null
    }
    return
  }
  confirmingDeleteId.value = key.id
  globalThis.setTimeout(() => {
    if (confirmingDeleteId.value === key.id) confirmingDeleteId.value = null
  }, 3000)
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="安全管理"
        title="Access Key"
        subtitle="管理调用路由器的下游设备和客户端凭证。"
      >
        <template #actions>
          <button
            data-testid="open-create-access-key"
            class="btn-primary"
            type="button"
            @click="dialogOpen = true"
          >
            <span class="flex items-center gap-2">
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
                  d="M12 4.5v15m7.5-7.5h-15"
                />
              </svg>
              创建 Access Key
            </span>
          </button>
        </template>
      </PageHeader>

      <div class="mt-5 overflow-hidden rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <StatePanel
          :loading="loading"
          :error="loadError"
          :empty="keys.length === 0"
          loadingLabel="Access Key 加载中…"
          emptyLabel="尚未创建 Access Key。"
          emptyHint="点击右上角「创建 Access Key」生成供客户端使用的凭证。"
          errorTestId="access-keys-load-error"
          retryTestId="access-keys-retry"
          @retry="loadKeys"
        >
          <AccessKeyCards
            :keys="keys"
            :busy-id="busyId"
            :confirming-revoke-id="confirmingRevokeId"
            :confirming-delete-id="confirmingDeleteId"
            @edit="openEditPolicy"
            @revoke="revokeKey"
            @delete="deleteKey"
          />
          <AccessKeyTable
            :keys="keys"
            :busy-id="busyId"
            :confirming-revoke-id="confirmingRevokeId"
            :confirming-delete-id="confirmingDeleteId"
            @edit="openEditPolicy"
            @revoke="revokeKey"
            @delete="deleteKey"
          />
        </StatePanel>
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
