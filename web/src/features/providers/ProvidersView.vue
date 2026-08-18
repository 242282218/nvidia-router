<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastSuccess } from '../../shared/toast'
import { useAsyncData } from '../../shared/useAsyncData'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import UiField from '../../shared/ui/UiField.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import { providersApi } from './api'
import type { ProviderCredential } from './types'

const { data: providers, loading, error: errorMessage, refresh: load, isDisposed } = useAsyncData<ProviderCredential[]>(
  async () => {
    const response: unknown = await providersApi.list()
    if (!isDataArrayResponse(response, isProvider)) throw new TypeError('Invalid providers response.')
    return response.data
  },
  { errorMessage: '提供商列表加载失败。' },
)

const providerList = computed<ProviderCredential[]>(() => providers.value ?? [])

const dialogOpen = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)

const createForm = ref({ name: '', base_url: '', key: '' })
const createError = ref('')

onMounted(() => {
  void load()
})

function isProvider(value: unknown): value is ProviderCredential {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.name === 'string'
    && typeof value.base_url === 'string'
    && typeof value.display_prefix === 'string'
    && typeof value.display_suffix === 'string'
    && typeof value.enabled === 'boolean'
    && typeof value.created_at === 'string'
}

function resetCreateForm(): void {
  createForm.value = { name: '', base_url: '', key: '' }
  createError.value = ''
}

function isValidBaseURL(raw: string): boolean {
  try {
    const parsed = new globalThis.URL(raw)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && parsed.hostname !== ''
  } catch {
    return false
  }
}

async function create(): Promise<void> {
  const name = createForm.value.name.trim()
  const baseUrl = createForm.value.base_url.trim()
  const key = createForm.value.key.trim()
  if (!name || !baseUrl || !key) {
    createError.value = '名称、Base URL 与 Key 均不能为空。'
    return
  }
  if (!isValidBaseURL(baseUrl)) {
    createError.value = 'Base URL 必须是有效的 HTTP 或 HTTPS 地址。'
    return
  }
  saving.value = true
  createError.value = ''
  try {
    await providersApi.create(name, baseUrl, key)
    dialogOpen.value = false
    resetCreateForm()
    await load()
    toastSuccess(`提供商「${name}」已创建。`)
  } catch (error) {
    createError.value = error instanceof ApiError ? error.message : '提供商创建失败。'
  } finally {
    saving.value = false
  }
}

async function toggle(provider: ProviderCredential): Promise<void> {
  busyId.value = provider.id
  try {
    await providersApi.setEnabled(provider.id, !provider.enabled)
    if (isDisposed()) return
    await load()
    toastSuccess(`提供商「${provider.name}」已${provider.enabled ? '停用' : '启用'}。`)
  } catch (error) {
    if (isDisposed()) return
    toastError(error instanceof ApiError ? error.message : '提供商状态更新失败。')
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

const enabledCount = computed(() => providerList.value.filter((p) => p.enabled).length)
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="提供商（OpenAI 兼容）"
        subtitle="当前运行时仅支持 NVIDIA；其他提供商暂时只展示已保存的元数据。"
      >
        <template #actions>
          <UiButton
            variant="secondary"
            icon="refresh"
            :loading="loading"
            loading-label="刷新中…"
            @click="load"
          >
            刷新
          </UiButton>
          <UiButton
            variant="primary"
            icon="plus"
            disabled
            title="当前运行时暂不支持非 NVIDIA 提供商"
            data-testid="open-create-provider"
            @click="dialogOpen = true"
          >
            新增提供商
          </UiButton>
        </template>
      </UiPageHeader>

      <div class="panel-inset mb-5 px-4 py-3 text-sm text-[var(--color-text-muted)]">
        当前运行时暂不支持非 NVIDIA 提供商，新增和启用操作已禁用。
      </div>

      <UiStatePanel
        :loading="loading"
        :error="errorMessage"
        :empty="providerList.length === 0"
        loadingLabel="提供商列表加载中…"
        skeleton="text"
        :skeleton-lines="3"
        emptyLabel="尚未配置 OpenAI 兼容提供商"
        emptyHint="NVIDIA 作为内置提供商始终可用，无需在此配置。"
        empty-icon="provider"
        @retry="load"
      >
        <div class="card overflow-hidden">
          <ul
            class="divide-y divide-[var(--color-border-subtle)]"
            aria-label="OpenAI 兼容提供商列表"
          >
            <li
              v-for="provider in providerList"
              :key="provider.id"
              class="flex flex-wrap items-center gap-3 px-5 py-4 transition-colors hover:bg-[var(--color-hover)]"
            >
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <p class="font-mono-data text-sm font-medium text-[var(--color-text)]">
                    {{ provider.name }}
                  </p>
                  <UiBadge
                    :variant="provider.enabled ? 'success' : 'muted'"
                    :label="provider.enabled ? '启用' : '停用'"
                  />
                </div>
                <p class="mt-1 truncate font-mono-data text-xs text-[var(--color-text-muted)]">
                  {{ provider.base_url }}
                </p>
                <p class="mt-0.5 font-mono-data text-xs text-[var(--color-text-subtle)]">
                  {{ provider.display_prefix }}…{{ provider.display_suffix }}
                </p>
              </div>
              <UiButton
                variant="secondary"
                size="sm"
                disabled
                title="当前运行时暂不支持非 NVIDIA 提供商"
                :data-testid="`toggle-provider-${provider.id}`"
                @click="toggle(provider)"
              >
                {{ provider.enabled ? '停用' : '启用' }}
              </UiButton>
            </li>
          </ul>
        </div>
      </UiStatePanel>

      <p class="mt-4 text-xs text-[var(--color-text-muted)]">
        当前 {{ enabledCount }} 个提供商已启用。提供商 Key 以主密钥加密存储，不落明文。
      </p>
    </div>

    <UiModal
      :open="dialogOpen"
      title="新增 OpenAI 兼容提供商"
      size="sm"
      @close="dialogOpen = false; resetCreateForm()"
    >
      <form
        class="space-y-4"
        data-testid="create-provider-form"
        novalidate
        @submit.prevent="create"
      >
        <UiField
          label="名称"
          input-id="provider-name"
          hint="字母、数字、下划线或连字符，最多 32 字符。"
        >
          <input
            id="provider-name"
            v-model="createForm.name"
            data-testid="provider-name"
            class="input-field"
            type="text"
            placeholder="siliconflow"
          >
        </UiField>
        <UiField
          label="Base URL"
          input-id="provider-base-url"
        >
          <input
            id="provider-base-url"
            v-model="createForm.base_url"
            data-testid="provider-base-url"
            class="input-field"
            type="url"
            placeholder="https://api.siliconflow.cn/v1"
          >
        </UiField>
        <UiField
          label="API Key"
          input-id="provider-key"
          hint="仅加密保存，除创建成功瞬间外不再展示。"
          :error="createError"
        >
          <input
            id="provider-key"
            v-model="createForm.key"
            data-testid="provider-key"
            class="input-field"
            type="password"
            autocomplete="off"
          >
        </UiField>
        <div class="flex justify-end gap-2">
          <UiButton
            variant="ghost"
            @click="dialogOpen = false; resetCreateForm()"
          >
            取消
          </UiButton>
          <UiButton
            variant="primary"
            type="submit"
            :loading="saving"
            loading-label="保存中…"
            data-testid="create-provider-submit"
          >
            保存
          </UiButton>
        </div>
      </form>
    </UiModal>
  </div>
</template>
