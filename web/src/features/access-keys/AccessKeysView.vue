<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastSuccess } from '../../shared/toast'
import { useAsyncData } from '../../shared/useAsyncData'
import UiButton from '../../shared/ui/UiButton.vue'
import UiConfirmDialog from '../../shared/ui/UiConfirmDialog.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import { accessKeysApi } from './api'
import AccessKeyCards from './AccessKeyCards.vue'
import AccessKeyTable from './AccessKeyTable.vue'
import CreateAccessKeyDialog from './CreateAccessKeyDialog.vue'
import EditAccessKeyPolicyDialog from './EditAccessKeyPolicyDialog.vue'
import type { AccessKey } from './types'

const { data: keys, loading, error: loadError, refresh: loadKeys, setData, isDisposed } = useAsyncData<AccessKey[]>(
  async () => {
    const response: unknown = await accessKeysApi.list()
    if (!isDataArrayResponse(response, isAccessKey)) {
      throw new TypeError('Invalid Access Key list response.')
    }
    return response.data
  },
  { errorMessage: 'Access Key 列表加载失败。' },
)

const keyList = computed<AccessKey[]>(() => keys.value ?? [])

const dialogOpen = ref(false)
const editDialogOpen = ref(false)
const editingKey = ref<AccessKey | null>(null)
const busyId = ref<number | null>(null)
// 撤销/删除共用一个确认对话框：pendingAction 描述「对哪条 Key 做什么」。
const pendingAction = ref<{ type: 'revoke' | 'delete'; key: AccessKey } | null>(null)
const acting = ref(false)

const route = (() => {
  try { return useRoute() } catch { return null as unknown as ReturnType<typeof useRoute> }
})()
const router = (() => {
  try { return useRouter() } catch { return null as unknown as ReturnType<typeof useRouter> }
})()

onMounted(() => {
  void loadKeys()
  if (route?.query.create === '1') {
    dialogOpen.value = true
    void router?.replace({ query: { ...(route.query as Record<string, string>), create: undefined } })
  }
})

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback
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

const confirmCopy = computed(() => {
  const action = pendingAction.value
  if (!action) return { title: '', message: '', confirmLabel: '确认' }
  if (action.type === 'revoke') {
    return {
      title: '撤销 Access Key',
      message: `将立即撤销「${action.key.name}」（${action.key.key_prefix}…）。撤销后使用该凭证的客户端立刻无法调用，此操作不可恢复。`,
      confirmLabel: '撤销',
    }
  }
  return {
    title: '删除 Access Key',
    message: `将永久删除「${action.key.name}」（${action.key.key_prefix}…）及其策略配置，此操作不可恢复。`,
    confirmLabel: '删除',
  }
})

async function confirmAction(): Promise<void> {
  const action = pendingAction.value
  if (!action || acting.value) return
  acting.value = true
  busyId.value = action.key.id
  try {
    if (action.type === 'revoke') {
      await accessKeysApi.revoke(action.key.id)
      if (isDisposed()) return
      pendingAction.value = null
      await loadKeys()
      toastSuccess(`Access Key「${action.key.name}」已撤销。`)
    } else {
      await accessKeysApi.delete(action.key.id)
      if (isDisposed()) return
      pendingAction.value = null
      setData(keyList.value.filter((item) => item.id !== action.key.id))
      toastSuccess(`Access Key「${action.key.name}」已删除。`)
    }
  } catch (error) {
    if (isDisposed()) return
    toastError(errorMessage(error, action.type === 'revoke' ? 'Access Key 撤销失败。' : 'Access Key 删除失败。'))
  } finally {
    if (!isDisposed()) {
      busyId.value = null
      acting.value = false
    }
  }
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="Access Key"
        subtitle="管理调用路由器的下游设备和客户端凭证。"
      >
        <template #actions>
          <UiButton
            data-testid="open-create-access-key"
            variant="primary"
            icon="plus"
            @click="dialogOpen = true"
          >
            创建 Access Key
          </UiButton>
        </template>
      </UiPageHeader>

      <UiStatePanel
        :loading="loading"
        :error="loadError"
        :empty="keyList.length === 0"
        loadingLabel="Access Key 加载中…"
        skeleton="table"
        emptyLabel="尚未创建 Access Key"
        emptyHint="点击右上角「创建 Access Key」生成供客户端使用的凭证。"
        empty-icon="access"
        errorTestId="access-keys-load-error"
        retryTestId="access-keys-retry"
        @retry="loadKeys"
      >
        <div class="card overflow-hidden">
          <AccessKeyCards
            :keys="keyList"
            :busy-id="busyId"
            @edit="openEditPolicy"
            @revoke="pendingAction = { type: 'revoke', key: $event }"
            @delete="pendingAction = { type: 'delete', key: $event }"
          />
          <AccessKeyTable
            :keys="keyList"
            :busy-id="busyId"
            @edit="openEditPolicy"
            @revoke="pendingAction = { type: 'revoke', key: $event }"
            @delete="pendingAction = { type: 'delete', key: $event }"
          />
        </div>
      </UiStatePanel>
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
    <UiConfirmDialog
      :open="pendingAction !== null"
      :title="confirmCopy.title"
      :message="confirmCopy.message"
      :confirm-label="confirmCopy.confirmLabel"
      :busy="acting"
      confirm-test-id="confirm-access-key-action"
      cancel-test-id="cancel-access-key-action"
      @confirm="confirmAction"
      @cancel="pendingAction = null"
    />
  </div>
</template>
