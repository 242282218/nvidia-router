<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import StatusBadge from '../../shared/components/StatusBadge.vue'
import { useDialog } from '../../shared/useDialog'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastSuccess } from '../../shared/toast'
import { providersApi } from './api'
import type { ProviderCredential } from './types'

const providers = ref<ProviderCredential[]>([])
const loading = ref(false)
const errorMessage = ref('')
const dialogOpen = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)
const dialogPanel = ref<globalThis.HTMLElement | null>(null)

// dialogOpen is a local ref, so Esc closes it directly and focus returns to the
// "新增提供商" trigger button on close.
useDialog(dialogOpen, dialogPanel, () => { dialogOpen.value = false })

const createForm = ref({ name: '', base_url: '', key: '' })
const createError = ref('')

let disposed = false

onMounted(() => {
  void load()
})

onBeforeUnmount(() => {
  disposed = true
})

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const response: unknown = await providersApi.list()
    if (disposed) return
    if (!isDataArrayResponse(response, isProvider)) throw new TypeError('Invalid providers response.')
    providers.value = response.data
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '提供商列表加载失败。'
  } finally {
    if (!disposed) loading.value = false
  }
}

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
  errorMessage.value = ''
  try {
    await providersApi.setEnabled(provider.id, !provider.enabled)
    if (disposed) return
    await load()
    toastSuccess(`提供商「${provider.name}」已${provider.enabled ? '停用' : '启用'}。`)
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '提供商状态更新失败。'
    toastError(errorMessage.value)
  } finally {
    if (!disposed) busyId.value = null
  }
}

const enabledCount = computed(() => providers.value.filter((p) => p.enabled).length)
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="运维管理"
        title="提供商（OpenAI 兼容）"
        subtitle="当前运行时仅支持 NVIDIA；其他提供商暂时只展示已保存的元数据。"
      >
        <template #actions>
          <button
            class="btn-primary"
            type="button"
            disabled
            title="当前运行时暂不支持非 NVIDIA 提供商"
            data-testid="open-create-provider"
            @click="dialogOpen = true"
          >
            新增提供商
          </button>
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

      <div class="mt-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-muted)] px-4 py-3 text-sm text-[var(--color-text-muted)]">
        当前运行时暂不支持非 NVIDIA 提供商，新增和启用操作已禁用。
      </div>

      <div class="card mt-5 overflow-hidden">
        <StatePanel
          :loading="loading"
          :error="errorMessage"
          :empty="providers.length === 0"
          loadingLabel="提供商列表加载中…"
          emptyLabel="尚未配置 OpenAI 兼容提供商。"
          emptyHint="NVIDIA 作为内置提供商始终可用，无需在此配置。"
          @retry="load"
        >
          <ul
            class="divide-y divide-[var(--color-border)]"
            aria-label="OpenAI 兼容提供商列表"
          >
            <li
              v-for="provider in providers"
              :key="provider.id"
              class="flex flex-wrap items-center gap-3 px-4 py-3 transition-colors hover:bg-[var(--color-hover)]"
            >
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <p class="font-mono text-sm font-medium text-[var(--color-text)]">
                    {{ provider.name }}
                  </p>
                  <StatusBadge
                    :variant="provider.enabled ? 'success' : 'muted'"
                    :label="provider.enabled ? '启用' : '停用'"
                  />
                </div>
                <p class="mt-0.5 truncate font-mono text-xs text-[var(--color-text-muted)]">
                  {{ provider.base_url }}
                </p>
                <p class="mt-0.5 font-mono text-xs text-[var(--color-text-subtle)]">
                  {{ provider.display_prefix }}…{{ provider.display_suffix }}
                </p>
              </div>
              <button
                class="btn-secondary"
                type="button"
                disabled
                title="当前运行时暂不支持非 NVIDIA 提供商"
                :data-testid="`toggle-provider-${provider.id}`"
                @click="toggle(provider)"
              >
                {{ provider.enabled ? '停用' : '启用' }}
              </button>
            </li>
          </ul>
        </StatePanel>
      </div>

      <div class="mt-4 text-xs text-[var(--color-text-muted)]">
        当前 {{ enabledCount }} 个提供商已启用。提供商 Key 以主密钥加密存储，不落明文。
      </div>
    </div>

    <Transition name="modal">
      <div
        v-if="dialogOpen"
        class="modal-overlay"
        role="dialog"
        aria-modal="true"
        aria-labelledby="create-provider-heading"
        @click.self="dialogOpen = false"
      >
        <section
          ref="dialogPanel"
          class="modal-panel max-w-lg"
        >
          <div class="border-b border-[var(--color-border)] px-6 py-4">
            <h2
              id="create-provider-heading"
              class="text-base font-semibold text-[var(--color-text)]"
            >
              新增 OpenAI 兼容提供商
            </h2>
          </div>
          <div class="p-6">
            <form
              class="space-y-4"
              data-testid="create-provider-form"
              novalidate
              @submit.prevent="create"
            >
              <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
                <span>名称</span>
                <input
                  v-model="createForm.name"
                  data-testid="provider-name"
                  class="input-field mt-1.5"
                  type="text"
                  placeholder="siliconflow"
                >
                <span class="mt-1 block text-xs text-[var(--color-text-muted)]">字母、数字、下划线或连字符，最多 32 字符。</span>
              </label>
              <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
                <span>Base URL</span>
                <input
                  v-model="createForm.base_url"
                  data-testid="provider-base-url"
                  class="input-field mt-1.5"
                  type="url"
                  placeholder="https://api.siliconflow.cn/v1"
                >
              </label>
              <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
                <span>API Key</span>
                <input
                  v-model="createForm.key"
                  data-testid="provider-key"
                  class="input-field mt-1.5"
                  type="password"
                  autocomplete="off"
                >
                <span class="mt-1 block text-xs text-[var(--color-text-muted)]">仅加密保存，除创建成功瞬间外不再展示。</span>
              </label>
              <Transition name="fade">
                <p
                  v-if="createError"
                  class="text-sm text-[var(--color-danger)]"
                  role="alert"
                >
                  {{ createError }}
                </p>
              </Transition>
              <div class="flex justify-end gap-3">
                <button
                  class="btn-secondary"
                  type="button"
                  @click="dialogOpen = false; resetCreateForm()"
                >
                  取消
                </button>
                <button
                  class="btn-primary"
                  type="submit"
                  :disabled="saving"
                  data-testid="create-provider-submit"
                >
                  {{ saving ? '保存中…' : '保存' }}
                </button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.modal-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.modal-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from section,
.modal-leave-to section {
  transform: scale(0.95);
}
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
