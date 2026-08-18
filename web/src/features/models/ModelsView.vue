<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { toastError, toastSuccess } from '../../shared/toast'
import { useAsyncData } from '../../shared/useAsyncData'
import UiButton from '../../shared/ui/UiButton.vue'
import UiCard from '../../shared/ui/UiCard.vue'
import UiConfirmDialog from '../../shared/ui/UiConfirmDialog.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import { modelsApi } from './api'
import ModelCards from './ModelCards.vue'
import ModelTable from './ModelTable.vue'
import type { Candidate, Model, SaveSelection } from './types'

const { data: models, loading, error: loadError, refresh: loadModels, isDisposed } = useAsyncData<Model[]>(
  async () => {
    const response: unknown = await modelsApi.list()
    if (!isDataArrayResponse(response, isModel)) {
      throw new TypeError('Invalid model list response.')
    }
    return response.data
  },
  { errorMessage: '模型列表加载失败。' },
)

const modelList = computed<Model[]>(() => models.value ?? [])

const candidates = ref<Candidate[]>([])
const selectedCandidates = ref<Record<string, boolean>>({})
const discovering = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)
const pendingDelete = ref<Model | null>(null)
const deleting = ref(false)
const errorMessage = ref('')
const candidateMessage = ref('')

onMounted(() => {
  void loadModels()
})

function isModel(value: unknown): value is Model {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.public_id === 'string'
    && typeof value.upstream_id === 'string'
    && typeof value.display_name === 'string'
    && typeof value.kind === 'string'
    && typeof value.enabled === 'boolean'
    && typeof value.supports_vision === 'boolean'
    && typeof value.supports_tools === 'boolean'
    && typeof value.supports_reasoning === 'boolean'
    && (value.blocked_by_key_ids === undefined
      || (Array.isArray(value.blocked_by_key_ids) && value.blocked_by_key_ids.every(isFiniteNumber)))
    && (value.capability_verified_at === undefined || typeof value.capability_verified_at === 'string')
    && (value.input_usd_per_mtok === undefined || typeof value.input_usd_per_mtok === 'number')
    && (value.output_usd_per_mtok === undefined || typeof value.output_usd_per_mtok === 'number')
    && (value.stream_first_token_timeout_ms === undefined || typeof value.stream_first_token_timeout_ms === 'number')
    && (value.stream_idle_timeout_ms === undefined || typeof value.stream_idle_timeout_ms === 'number')
    && (value.reasoning_wire_format === undefined || typeof value.reasoning_wire_format === 'string')
}

function isCandidate(value: unknown): value is Candidate {
  return isRecord(value)
    && typeof value.upstream_id === 'string'
    && typeof value.display_name === 'string'
    && typeof value.kind === 'string'
    && typeof value.supports_vision === 'boolean'
    && typeof value.supports_tools === 'boolean'
    && typeof value.supports_reasoning === 'boolean'
    && (value.reasoning_wire_format === undefined || typeof value.reasoning_wire_format === 'string')
}

async function discover(): Promise<void> {
  discovering.value = true
  candidateMessage.value = ''
  errorMessage.value = ''
  try {
    const response: unknown = await modelsApi.candidates()
    if (isDisposed()) return
    if (!isDataArrayResponse(response, isCandidate)) {
      throw new TypeError('Invalid model candidates response.')
    }
    candidates.value = response.data
    const configured = new Set(modelList.value.map((model) => model.upstream_id))
    selectedCandidates.value = Object.fromEntries(candidates.value.map((candidate) => [candidate.upstream_id, configured.has(candidate.upstream_id)]))
    candidateMessage.value = `发现 ${candidates.value.length} 个候选模型。`
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '候选模型发现失败。'
  } finally {
    if (!isDisposed()) discovering.value = false
  }
}

function selectionFor(candidate: Candidate): SaveSelection {
  return {
    ...candidate,
    public_id: candidate.upstream_id,
    enabled: false,
  }
}

async function saveCandidates(): Promise<void> {
  const configured = new Set(modelList.value.map((model) => model.upstream_id))
  const selected = candidates.value
    .filter((candidate) => selectedCandidates.value[candidate.upstream_id] && !configured.has(candidate.upstream_id))
    .map(selectionFor)
  saving.value = true
  errorMessage.value = ''
  try {
    await modelsApi.save(selected)
    if (isDisposed()) return
    await loadModels()
    if (isDisposed()) return
    // Re-sync the candidate checkboxes: models that were just saved are now
    // configured, so re-submitting would filter them out anyway; reflect that
    // state instead of leaving stale "new" selections that invite a no-op save.
    const nextConfigured = new Set(modelList.value.map((model) => model.upstream_id))
    selectedCandidates.value = Object.fromEntries(candidates.value.map((candidate) => [candidate.upstream_id, nextConfigured.has(candidate.upstream_id)]))
    candidateMessage.value = `已保存 ${selected.length} 个模型。`
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '保存模型白名单失败。'
  } finally {
    if (!isDisposed()) saving.value = false
  }
}

async function toggleModel(model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    const updated: unknown = await modelsApi.patch(model.id, { enabled: !model.enabled })
    if (isDisposed()) return
    if (!isModel(updated)) {
      throw new TypeError('Invalid model patch response.')
    }
    replaceModel(updated)
    toastSuccess(`模型「${updated.display_name}」已${updated.enabled ? '启用' : '停用'}。`)
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '更新模型状态失败。'
    toastError(errorMessage.value)
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

async function unblockModel(keyId: number, model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    await modelsApi.unblock(keyId, model.id)
    if (isDisposed()) return
    await loadModels()
    toastSuccess(`模型「${model.display_name}」已解除阻断。`)
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '模型 block 恢复失败。'
    toastError(errorMessage.value)
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

function replaceModel(updated: Model): void {
  const index = modelList.value.findIndex((model) => model.id === updated.id)
  if (index >= 0 && models.value) models.value[index] = updated
}

async function savePricing(model: Model, inputUsd: number, outputUsd: number): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    const updated: unknown = await modelsApi.patch(model.id, {
      input_usd_per_mtok: inputUsd,
      output_usd_per_mtok: outputUsd,
    })
    if (isDisposed()) return
    if (!isModel(updated)) {
      throw new TypeError('Invalid model patch response.')
    }
    replaceModel(updated)
    toastSuccess(`模型「${updated.display_name}」单价已更新。`)
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '保存模型单价失败。'
    toastError(errorMessage.value)
  } finally {
    if (!isDisposed()) busyId.value = null
  }
}

async function confirmDelete(): Promise<void> {
  const model = pendingDelete.value
  if (!model || deleting.value) return
  deleting.value = true
  busyId.value = model.id
  try {
    await modelsApi.delete(model.id)
    if (isDisposed()) return
    pendingDelete.value = null
    if (models.value) {
      models.value = models.value.filter((item) => item.id !== model.id)
    }
    toastSuccess(`模型「${model.display_name}」已从白名单中删除。`)
  } catch (error) {
    if (isDisposed()) return
    errorMessage.value = error instanceof ApiError ? error.message : '删除模型失败。'
    toastError(errorMessage.value)
  } finally {
    if (!isDisposed()) {
      busyId.value = null
      deleting.value = false
    }
  }
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="模型白名单"
        subtitle="管理模型类型、能力标签和启用状态。"
      >
        <template #actions>
          <UiButton
            data-testid="discover-models"
            variant="secondary"
            icon="search"
            :loading="discovering"
            loading-label="发现中…"
            @click="discover"
          >
            发现候选模型
          </UiButton>
        </template>
      </UiPageHeader>

      <!-- Candidates section -->
      <Transition name="slide">
        <UiCard
          v-if="candidates.length"
          class="mb-5"
          title="候选模型"
          subtitle="从首个可用 NVIDIA Key 获取，勾选后保存白名单。"
        >
          <template #actions>
            <UiButton
              data-testid="save-candidates"
              variant="primary"
              :loading="saving"
              loading-label="保存中…"
              @click="saveCandidates"
            >
              保存选择
            </UiButton>
          </template>
          <div class="grid gap-2 sm:grid-cols-2">
            <label
              v-for="candidate in candidates"
              :key="candidate.upstream_id"
              class="flex cursor-pointer items-start gap-3 rounded-[var(--radius-control)] border border-[var(--color-border)] p-3 text-sm transition-colors hover:bg-[var(--color-hover)]"
            >
              <input
                v-model="selectedCandidates[candidate.upstream_id]"
                class="mt-0.5 h-4 w-4 rounded border-[var(--color-text-subtle)] bg-[var(--color-sunken)] text-[var(--color-accent)] focus:ring-[color-mix(in_srgb,var(--color-accent)_30%,transparent)]"
                :data-testid="`candidate-${candidate.upstream_id}`"
                type="checkbox"
              >
              <span>
                <span class="font-medium text-[var(--color-text)]">{{ candidate.display_name }}</span>
                <span class="badge-info ml-2">{{ candidate.kind }}</span>
                <span class="mt-0.5 block font-mono-data text-xs text-[var(--color-text-muted)]">{{ candidate.upstream_id }}</span>
              </span>
            </label>
          </div>
        </UiCard>
      </Transition>

      <!-- Messages -->
      <Transition name="slide">
        <p
          v-if="candidateMessage"
          class="badge-success mb-4 inline-flex px-3 py-1 text-sm"
        >
          {{ candidateMessage }}
        </p>
      </Transition>
      <Transition name="slide">
        <p
          v-if="errorMessage"
          class="mb-4 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ errorMessage }}
        </p>
      </Transition>

      <p
        data-testid="mobile-model-hint"
        class="panel-inset mb-4 px-3 py-2 text-xs text-[var(--color-text-muted)] md:hidden"
      >
        移动端可查看和切换模型状态；候选模型的批量选择等高级操作请在桌面端或上方完成。
      </p>

      <!-- Model list -->
      <UiStatePanel
        :loading="loading"
        :error="loadError"
        :empty="modelList.length === 0"
        loadingLabel="模型列表加载中…"
        skeleton="table"
        emptyLabel="白名单暂无模型"
        emptyHint="点击右上角「发现候选模型」从上游拉取可用模型并勾选保存。"
        empty-icon="model"
        errorTestId="models-load-error"
        retryTestId="models-retry"
        @retry="loadModels"
      >
        <ModelTable
          :models="modelList"
          :busy-id="busyId"
          @toggle="toggleModel"
          @unblock="unblockModel"
          @save-pricing="savePricing"
          @delete="pendingDelete = $event"
        />
        <ModelCards
          :models="modelList"
          :busy-id="busyId"
          @toggle="toggleModel"
          @unblock="unblockModel"
          @delete="pendingDelete = $event"
        />
      </UiStatePanel>
    </div>

    <UiConfirmDialog
      :open="pendingDelete !== null"
      title="删除模型"
      :message="pendingDelete ? `将「${pendingDelete.display_name}」从白名单中删除。删除后客户端无法再按公开 ID 调用该模型。` : ''"
      confirm-label="删除"
      :busy="deleting"
      confirm-test-id="confirm-delete-model"
      cancel-test-id="cancel-delete-model"
      @confirm="confirmDelete"
      @cancel="pendingDelete = null"
    />
  </div>
</template>

<style scoped>
.slide-enter-active {
  transition: opacity 0.25s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.25s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.slide-leave-active {
  transition: opacity 0.18s cubic-bezier(0.4, 0.0, 1, 1), transform 0.18s cubic-bezier(0.4, 0.0, 1, 1);
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
