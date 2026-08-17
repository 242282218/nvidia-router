<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import { toastError, toastSuccess } from '../../shared/toast'
import { modelsApi } from './api'
import ModelCards from './ModelCards.vue'
import ModelTable from './ModelTable.vue'
import type { Candidate, Model, SaveSelection } from './types'

const models = ref<Model[]>([])
const candidates = ref<Candidate[]>([])
const selectedCandidates = ref<Record<string, boolean>>({})
const loading = ref(false)
const loadError = ref('')
const discovering = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)
const confirmingId = ref<number | null>(null)
const errorMessage = ref('')
const candidateMessage = ref('')
let loadSequence = 0
let disposed = false

onMounted(() => {
  void loadModels()
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
})

async function loadModels(): Promise<void> {
  if (disposed) return
  const sequence = ++loadSequence
  loading.value = true
  loadError.value = ''
  try {
    const response: unknown = await modelsApi.list()
    if (disposed || sequence !== loadSequence) return
    if (!isDataArrayResponse(response, isModel)) {
      throw new TypeError('Invalid model list response.')
    }
    models.value = response.data
    errorMessage.value = ''
  } catch (error) {
    if (disposed || sequence !== loadSequence) return
    // A failed load must not read as "no models configured": the empty table
    // would invite a pointless re-discovery. The error panel (not the
    // action-error alert) carries the message.
    loadError.value = error instanceof ApiError ? error.message : '模型列表加载失败。'
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

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
    if (disposed) return
    if (!isDataArrayResponse(response, isCandidate)) {
      throw new TypeError('Invalid model candidates response.')
    }
    candidates.value = response.data
    const configured = new Set(models.value.map((model) => model.upstream_id))
    selectedCandidates.value = Object.fromEntries(candidates.value.map((candidate) => [candidate.upstream_id, configured.has(candidate.upstream_id)]))
    candidateMessage.value = `发现 ${candidates.value.length} 个候选模型。`
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '候选模型发现失败。'
  } finally {
    if (!disposed) discovering.value = false
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
  const configured = new Set(models.value.map((model) => model.upstream_id))
  const selected = candidates.value
    .filter((candidate) => selectedCandidates.value[candidate.upstream_id] && !configured.has(candidate.upstream_id))
    .map(selectionFor)
  saving.value = true
  errorMessage.value = ''
  try {
    await modelsApi.save(selected)
    if (disposed) return
    await loadModels()
    if (disposed) return
    // Re-sync the candidate checkboxes: models that were just saved are now
    // configured, so re-submitting would filter them out anyway; reflect that
    // state instead of leaving stale "new" selections that invite a no-op save.
    const nextConfigured = new Set(models.value.map((model) => model.upstream_id))
    selectedCandidates.value = Object.fromEntries(candidates.value.map((candidate) => [candidate.upstream_id, nextConfigured.has(candidate.upstream_id)]))
    candidateMessage.value = `已保存 ${selected.length} 个模型。`
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '保存模型白名单失败。'
  } finally {
    if (!disposed) saving.value = false
  }
}

async function toggleModel(model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    const updated: unknown = await modelsApi.patch(model.id, { enabled: !model.enabled })
    if (disposed) return
    if (!isModel(updated)) {
      throw new TypeError('Invalid model patch response.')
    }
    replaceModel(updated)
    toastSuccess(`模型「${updated.display_name}」已${updated.enabled ? '启用' : '停用'}。`)
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '更新模型状态失败。'
    toastError(errorMessage.value)
  } finally {
    if (!disposed) busyId.value = null
  }
}

async function unblockModel(keyId: number, model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    await modelsApi.unblock(keyId, model.id)
    if (disposed) return
    await loadModels()
    toastSuccess(`模型「${model.display_name}」已解除阻断。`)
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '模型 block 恢复失败。'
    toastError(errorMessage.value)
  } finally {
    if (!disposed) busyId.value = null
  }
}

function replaceModel(updated: Model): void {
  const index = models.value.findIndex((model) => model.id === updated.id)
  if (index >= 0) models.value[index] = updated
}

async function savePricing(model: Model, inputUsd: number, outputUsd: number): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    const updated: unknown = await modelsApi.patch(model.id, {
      input_usd_per_mtok: inputUsd,
      output_usd_per_mtok: outputUsd,
    })
    if (disposed) return
    if (!isModel(updated)) {
      throw new TypeError('Invalid model patch response.')
    }
    replaceModel(updated)
    toastSuccess(`模型「${updated.display_name}」单价已更新。`)
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '保存模型单价失败。'
    toastError(errorMessage.value)
  } finally {
    if (!disposed) busyId.value = null
  }
}

async function deleteModel(model: Model): Promise<void> {
  if (busyId.value === model.id) return
  if (confirmingId.value === model.id) {
    confirmingId.value = null
    busyId.value = model.id
    try {
      await modelsApi.delete(model.id)
      if (disposed) return
      models.value = models.value.filter((item) => item.id !== model.id)
      toastSuccess(`模型「${model.display_name}」已从白名单中删除。`)
    } catch (error) {
      if (disposed) return
      errorMessage.value = error instanceof ApiError ? error.message : '删除模型失败。'
      toastError(errorMessage.value)
    } finally {
      if (!disposed) busyId.value = null
    }
    return
  }
  confirmingId.value = model.id
  globalThis.setTimeout(() => {
    if (confirmingId.value === model.id) confirmingId.value = null
  }, 3000)
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <PageHeader
        eyebrow="运维管理"
        title="模型白名单"
        subtitle="管理模型类型、能力标签和启用状态。"
      >
        <template #actions>
          <button
            class="btn-secondary"
            data-testid="discover-models"
            type="button"
            :disabled="discovering"
            @click="discover"
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
                  d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
                />
              </svg>
              {{ discovering ? '发现中…' : '发现候选模型' }}
            </span>
          </button>
        </template>
      </PageHeader>

      <!-- Candidates section -->
      <Transition name="slide">
        <section
          v-if="candidates.length"
          class="card p-5 mb-5 animate-slide-up"
        >
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-sm font-medium text-[var(--color-text)]">
                候选模型
              </h2>
              <p class="mt-1 text-sm text-[var(--color-text-muted)]">
                从首个可用 NVIDIA Key 获取，勾选后保存白名单。
              </p>
            </div>
            <button
              class="btn-primary"
              data-testid="save-candidates"
              type="button"
              :disabled="saving"
              @click="saveCandidates"
            >
              {{ saving ? '保存中…' : '保存选择' }}
            </button>
          </div>
          <div class="mt-4 grid gap-2 sm:grid-cols-2">
            <label
              v-for="candidate in candidates"
              :key="candidate.upstream_id"
              class="flex items-start gap-3 rounded-lg border border-[var(--color-border)] p-3 text-sm hover:bg-[color-mix(in_srgb,var(--color-border)_30%,transparent)] transition-colors cursor-pointer"
            >
              <input
                v-model="selectedCandidates[candidate.upstream_id]"
                class="mt-0.5 h-4 w-4 rounded border-[var(--color-text-subtle)] bg-[var(--color-sunken)] text-[var(--color-accent)] focus:ring-[color-mix(in_srgb,var(--color-accent)_30%,transparent)]"
                :data-testid="`candidate-${candidate.upstream_id}`"
                type="checkbox"
              >
              <span>
                <span class="font-medium text-[var(--color-text)]">{{ candidate.display_name }}</span>
                <span class="ml-2 badge-info">{{ candidate.kind }}</span>
                <span class="mt-0.5 block font-mono text-xs text-[var(--color-text-muted)]">{{ candidate.upstream_id }}</span>
              </span>
            </label>
          </div>
        </section>
      </Transition>

      <!-- Messages -->
      <Transition name="slide">
        <p
          v-if="candidateMessage"
          class="mb-4 text-sm badge-success inline-flex px-3 py-1"
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
        class="mt-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-sunken)] px-3 py-2 text-xs text-[var(--color-text-muted)] md:hidden"
      >
        移动端可查看和切换模型状态；候选模型的批量选择等高级操作请在桌面端或上方完成。
      </p>

      <!-- Model list -->
      <div class="mt-4">
        <StatePanel
          :loading="loading"
          :error="loadError"
          loadingLabel="模型列表加载中…"
          errorTestId="models-load-error"
          retryTestId="models-retry"
          @retry="loadModels"
        >
          <ModelTable
            :models="models"
            :busy-id="busyId"
            :confirming-id="confirmingId"
            @toggle="toggleModel"
            @unblock="unblockModel"
            @save-pricing="savePricing"
            @delete="deleteModel"
          />
          <ModelCards
            :models="models"
            :busy-id="busyId"
            :confirming-id="confirmingId"
            @toggle="toggleModel"
            @unblock="unblockModel"
            @delete="deleteModel"
          />
        </StatePanel>
      </div>
    </div>
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
