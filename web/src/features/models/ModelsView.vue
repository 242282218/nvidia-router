<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { ApiError } from '../../shared/api/client'
import { modelsApi } from './api'
import ModelCards from './ModelCards.vue'
import ModelTable from './ModelTable.vue'
import type { Candidate, Model, SaveSelection } from './types'

const models = ref<Model[]>([])
const candidates = ref<Candidate[]>([])
const selectedCandidates = ref<Record<string, boolean>>({})
const loading = ref(false)
const discovering = ref(false)
const saving = ref(false)
const busyId = ref<number | null>(null)
const errorMessage = ref('')
const candidateMessage = ref('')

onMounted(() => {
  void loadModels()
})

async function loadModels(): Promise<void> {
  loading.value = true
  try {
    models.value = (await modelsApi.list()).data
    errorMessage.value = ''
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '模型列表加载失败。'
  } finally {
    loading.value = false
  }
}

async function discover(): Promise<void> {
  discovering.value = true
  candidateMessage.value = ''
  errorMessage.value = ''
  try {
    candidates.value = (await modelsApi.candidates()).data
    const configured = new Set(models.value.map((model) => model.upstream_id))
    selectedCandidates.value = Object.fromEntries(candidates.value.map((candidate) => [candidate.upstream_id, configured.has(candidate.upstream_id)]))
    candidateMessage.value = `发现 ${candidates.value.length} 个候选模型。`
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '候选模型发现失败。'
  } finally {
    discovering.value = false
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
    await loadModels()
    candidateMessage.value = `已保存 ${selected.length} 个模型。`
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '保存模型白名单失败。'
  } finally {
    saving.value = false
  }
}

async function toggleModel(model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    const updated = await modelsApi.patch(model.id, { enabled: !model.enabled })
    replaceModel(updated)
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '更新模型状态失败。'
  } finally {
    busyId.value = null
  }
}

async function unblockModel(keyId: number, model: Model): Promise<void> {
  busyId.value = model.id
  errorMessage.value = ''
  try {
    await modelsApi.unblock(keyId, model.id)
    await loadModels()
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '模型 block 恢复失败。'
  } finally {
    busyId.value = null
  }
}

function replaceModel(updated: Model): void {
  const index = models.value.findIndex((model) => model.id === updated.id)
  if (index >= 0) models.value[index] = updated
}
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-4 text-slate-100 sm:p-6">
    <section class="mx-auto max-w-6xl">
      <header class="rounded-xl bg-slate-900 px-5 py-5 shadow-xl sm:px-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-sm text-indigo-300">
              运维管理
            </p><h1 class="mt-1 text-2xl font-semibold">
              模型白名单
            </h1><p class="mt-2 text-sm text-slate-400">
              管理模型类型、能力标签和启用状态。
            </p>
          </div>
          <button
            class="rounded-lg border border-slate-700 px-3 py-2 text-sm hover:border-slate-500 disabled:opacity-50"
            data-testid="discover-models"
            type="button"
            :disabled="discovering"
            @click="discover"
          >
            发现候选模型
          </button>
        </div>
      </header>

      <section
        v-if="candidates.length"
        class="mt-5 rounded-xl border border-slate-800 bg-slate-900 p-5"
      >
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="font-medium">
              候选模型
            </h2><p class="mt-1 text-sm text-slate-400">
              从首个可用 NVIDIA Key 获取，勾选后保存白名单。
            </p>
          </div><button
            class="rounded-lg bg-indigo-500 px-3 py-2 text-sm disabled:opacity-50"
            data-testid="save-candidates"
            type="button"
            :disabled="saving"
            @click="saveCandidates"
          >
            保存选择
          </button>
        </div>
        <div class="mt-4 grid gap-2 sm:grid-cols-2">
          <label
            v-for="candidate in candidates"
            :key="candidate.upstream_id"
            class="flex items-start gap-3 rounded-lg border border-slate-800 p-3 text-sm"
          ><input
            v-model="selectedCandidates[candidate.upstream_id]"
            class="mt-1"
            :data-testid="`candidate-${candidate.upstream_id}`"
            type="checkbox"
          ><span><span class="font-medium">{{ candidate.display_name }}</span><span class="ml-2 font-mono text-xs text-indigo-300">{{ candidate.kind }}</span><span class="mt-1 block font-mono text-xs text-slate-500">{{ candidate.upstream_id }}</span></span></label>
        </div>
      </section>
      <p
        v-if="candidateMessage"
        class="mt-3 text-sm text-emerald-300"
      >
        {{ candidateMessage }}
      </p>
      <p
        v-if="errorMessage"
        class="mt-3 text-sm text-rose-300"
        role="alert"
      >
        {{ errorMessage }}
      </p>

      <p
        data-testid="mobile-model-hint"
        class="mt-4 text-xs text-slate-500 md:hidden"
      >
        候选模型批量选择等高级操作请在桌面端完成。
      </p>

      <section class="mt-5">
        <div
          v-if="loading"
          class="rounded-xl border border-slate-800 bg-slate-900 p-6 text-sm text-slate-400"
        >
          加载中……
        </div>
        <template v-else>
          <ModelTable
            :models="models"
            :busy-id="busyId"
            @toggle="toggleModel"
            @unblock="unblockModel"
          /><ModelCards
            :models="models"
            :busy-id="busyId"
            @toggle="toggleModel"
            @unblock="unblockModel"
          />
        </template>
      </section>
    </section>
  </main>
</template>
