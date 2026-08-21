<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { ApiError, isFiniteNumber, isRecord, isAbortError } from '../../shared/api/client'
import { formatTimeOfDay } from '../../shared/format'
import { toastError, toastSuccess } from '../../shared/toast'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiSelect from '../../shared/ui/UiSelect.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import UiSwitch from '../../shared/ui/UiSwitch.vue'
import { usePolling } from '../../shared/usePolling'
import { modelHealthApi } from './api'
import ModelHealthCard from './ModelHealthCard.vue'
import type {
  ModelHealthGroup,
  ModelHealthModel,
  ModelHealthRange,
  ModelHealthSettings,
  ModelHealthSort,
  ModelHealthSummary,
} from './types'

const ranges: Array<{ value: ModelHealthRange; label: string }> = [
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
]

const groups: Array<{ value: ModelHealthGroup; label: string }> = [
  { value: 'default', label: '默认分组' },
  { value: 'provider', label: '按提供商' },
  { value: 'kind', label: '按模型类型' },
]

const sorts: Array<{ value: ModelHealthSort; label: string }> = [
  { value: 'availability', label: '可用优先' },
  { value: 'recent', label: '最近检测优先' },
  { value: 'volume', label: '探测量优先' },
]

const range = ref<ModelHealthRange>('6h')
const group = ref<ModelHealthGroup>('default')
const sort = ref<ModelHealthSort>('availability')
const scope = ref<'all' | 'enabled'>('all')
const search = ref('')
const summary = ref<ModelHealthSummary | null>(null)
const loading = ref(false)
const saving = ref(false)
const running = ref(false)
const errorMessage = ref('')
const saveError = ref('')
const updatedAt = ref<Date | null>(null)
let disposed = false
let loadSequence = 0
let loadController: globalThis.AbortController | null = null
let saveController: globalThis.AbortController | null = null

const currentSettings = computed<ModelHealthSettings>(() => summary.value?.settings ?? {
  enabled: false,
  interval_seconds: 60,
  concurrency: 2,
})

const intervalValue = computed({
  get: () => String(currentSettings.value.interval_seconds),
  set: (value: string) => { void updateSettings({ interval_seconds: Number(value) }) },
})

const filteredModels = computed<ModelHealthModel[]>(() => {
  const needle = search.value.trim().toLowerCase()
  return (summary.value?.models ?? []).filter((model) => {
    if (scope.value === 'enabled' && !model.enabled) return false
    if (!needle) return true
    return [model.display_name, model.public_id, model.provider, model.kind]
      .some((value) => value.toLowerCase().includes(needle))
  })
})

const groupedModels = computed(() => {
  if (group.value === 'default') return [{ key: 'all', label: '全部模型', models: filteredModels.value }]
  const groupsByKey = new Map<string, ModelHealthModel[]>()
  for (const model of filteredModels.value) {
    const key = group.value === 'provider' ? model.provider : model.kind
    const list = groupsByKey.get(key) ?? []
    list.push(model)
    groupsByKey.set(key, list)
  }
  return [...groupsByKey.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, models]) => ({ key, label: groupLabel(key), models }))
})

const totalProbes = computed(() => (summary.value?.models ?? []).reduce((total, model) => total + model.probe_count, 0))

watch([range, group, sort], () => {
  void loadSummary(false)
})

onMounted(() => {
  void loadSummary(true)
})

// The backend probe cadence is operator-controlled; this lighter UI poll only
// fetches the persisted cards and pauses automatically in hidden tabs.
usePolling(() => loadSummary(false), 30_000)

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
  loadController?.abort()
  saveController?.abort()
})

async function loadSummary(showLoading: boolean): Promise<void> {
  if (disposed) return
  loadController?.abort()
  const controller = new globalThis.AbortController()
  loadController = controller
  const sequence = ++loadSequence
  if (!summary.value || showLoading) loading.value = true
  errorMessage.value = ''
  try {
    const response: unknown = await modelHealthApi.getSummary(range.value, group.value, sort.value, controller.signal)
    if (disposed || sequence !== loadSequence) return
    if (!isModelHealthSummaryResponse(response)) throw new TypeError('Invalid model health response.')
    summary.value = response.data
    updatedAt.value = new Date()
  } catch (error) {
    if (disposed || sequence !== loadSequence || isAbortError(error)) return
    errorMessage.value = error instanceof ApiError ? error.message : '渠道状态加载失败。'
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

async function updateSettings(patch: Partial<Pick<ModelHealthSettings, 'enabled' | 'interval_seconds' | 'concurrency'>>): Promise<void> {
  if (disposed || saving.value) return
  const current = currentSettings.value
  const next = {
    enabled: patch.enabled ?? current.enabled,
    interval_seconds: patch.interval_seconds ?? current.interval_seconds,
    concurrency: patch.concurrency ?? current.concurrency,
  }
  if (!Number.isInteger(next.interval_seconds) || next.interval_seconds < 10 || next.interval_seconds > 3600) {
    saveError.value = '检测频率必须在 10 秒到 1 小时之间。'
    return
  }
  saving.value = true
  saveError.value = ''
  saveController?.abort()
  const controller = new globalThis.AbortController()
  saveController = controller
  try {
    const response = await modelHealthApi.updateSettings(next, controller.signal)
    if (disposed) return
    if (summary.value) summary.value.settings = response.data
    toastSuccess('渠道状态设置已保存。')
  } catch (error) {
    if (disposed || isAbortError(error)) return
    saveError.value = error instanceof ApiError ? error.message : '渠道状态设置保存失败。'
    toastError(saveError.value)
  } finally {
    if (!disposed && saveController === controller) saving.value = false
  }
}

async function runNow(): Promise<void> {
  if (disposed || running.value) return
  running.value = true
  saveError.value = ''
  try {
    await modelHealthApi.runNow()
    toastSuccess('已开始检测全部白名单模型。')
    await loadSummary(false)
  } catch (error) {
    if (disposed || isAbortError(error)) return
    const message = error instanceof ApiError ? error.message : '立即检测失败。'
    saveError.value = message
    toastError(message)
  } finally {
    if (!disposed) running.value = false
  }
}

function groupLabel(value: string): string {
  if (group.value === 'provider') return value === 'opencodefree' ? 'OpenCodeFree' : value.toUpperCase()
  return value
}

function isModelHealthSummaryResponse(value: unknown): value is { data: ModelHealthSummary } {
  if (!isRecord(value) || !isRecord(value.data)) return false
  const data = value.data
  return (data.range === '1h' || data.range === '6h' || data.range === '24h' || data.range === '7d')
    && typeof data.from === 'string'
    && typeof data.to === 'string'
    && isFiniteNumber(data.total_models)
    && isFiniteNumber(data.healthy_count)
    && isFiniteNumber(data.degraded_count)
    && isFiniteNumber(data.unavailable_count)
    && isFiniteNumber(data.unchecked_count)
    && isFiniteNumber(data.stale_count)
    && isFiniteNumber(data.unconfigured_count)
    && Array.isArray(data.models)
    && data.models.every(isModelHealthModel)
    && isModelHealthSettings(data.settings)
}

function isModelHealthSettings(value: unknown): value is ModelHealthSettings {
  return isRecord(value)
    && typeof value.enabled === 'boolean'
    && isFiniteNumber(value.interval_seconds)
    && isFiniteNumber(value.concurrency)
}

function isModelHealthModel(value: unknown): value is ModelHealthModel {
  return isRecord(value)
    && isFiniteNumber(value.model_id)
    && typeof value.public_id === 'string'
    && typeof value.display_name === 'string'
    && typeof value.kind === 'string'
    && typeof value.provider === 'string'
    && typeof value.enabled === 'boolean'
    && typeof value.status === 'string'
    && isFiniteNumber(value.success_rate)
    && isFiniteNumber(value.probe_count)
    && isFiniteNumber(value.success_count)
    && isFiniteNumber(value.failure_count)
    && isFiniteNumber(value.timeout_count)
    && isFiniteNumber(value.skipped_count)
    && isFiniteNumber(value.consecutive_failures)
    && Array.isArray(value.buckets)
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="渠道状态"
        subtitle="主动检测模型白名单中的全部模型，状态与真实请求监控分离。"
      >
        <template #actions>
          <UiButton
            data-testid="model-health-run"
            variant="primary"
            icon="play"
            :loading="running"
            loading-label="检测中…"
            @click="runNow"
          >
            立即检测
          </UiButton>
          <UiButton
            variant="secondary"
            icon="refresh"
            :loading="loading"
            loading-label="刷新中…"
            data-testid="model-health-refresh"
            @click="loadSummary(false)"
          >
            刷新
          </UiButton>
        </template>
      </UiPageHeader>

      <div class="flex flex-wrap items-center gap-2.5">
        <UiSelect
          v-model="range"
          data-testid="model-health-range"
          aria-label="渠道状态时间范围"
          class="w-28"
        >
          <option
            v-for="option in ranges"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </option>
        </UiSelect>
        <UiSelect
          v-model="group"
          data-testid="model-health-group"
          aria-label="渠道状态分组"
          class="w-32"
        >
          <option
            v-for="option in groups"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </option>
        </UiSelect>
        <UiSelect
          v-model="scope"
          data-testid="model-health-scope"
          aria-label="渠道状态范围"
          class="w-32"
        >
          <option value="all">
            全部模型
          </option>
          <option value="enabled">
            仅启用模型
          </option>
        </UiSelect>
        <UiSelect
          v-model="sort"
          data-testid="model-health-sort"
          aria-label="渠道状态排序"
          class="w-36"
        >
          <option
            v-for="option in sorts"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </option>
        </UiSelect>

        <div class="ml-auto flex items-center gap-2 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5">
          <UiSwitch
            data-testid="model-health-enabled"
            :checked="currentSettings.enabled"
            :disabled="saving"
            label="启用自动模型检测"
            @change="(value) => updateSettings({ enabled: value })"
          />
          <span class="text-sm text-[var(--color-text-secondary)]">自动检测</span>
          <input
            v-model.lazy="intervalValue"
            type="number"
            min="10"
            max="3600"
            step="1"
            data-testid="model-health-interval"
            aria-label="模型检测频率（秒）"
            class="input-field w-24"
          >
          <span class="text-xs text-[var(--color-text-muted)]">秒</span>
        </div>
      </div>

      <p class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--color-text-muted)]">
        <span>监控 {{ summary?.total_models ?? 0 }} 个模型 · 总探测 {{ totalProbes }}</span>
        <span v-if="summary">正常 {{ summary.healthy_count }} · 降级 {{ summary.degraded_count }} · 过期 {{ summary.stale_count }} · 未检测 {{ summary.unchecked_count }}</span>
        <span v-if="updatedAt">更新于 {{ formatTimeOfDay(updatedAt) }}</span>
        <span>主动探测为只读请求，不计入请求监控；频率越短，上游调用越多。</span>
      </p>

      <p
        v-if="saveError"
        data-testid="model-health-save-error"
        class="mt-3 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ saveError }}
      </p>

      <p
        v-if="summary && errorMessage"
        data-testid="model-health-stale"
        class="mt-3 flex flex-wrap items-center gap-3 text-sm text-[var(--color-warning)]"
        role="status"
      >
        当前显示的是上一次成功加载的数据。
        <UiButton
          variant="secondary"
          size="sm"
          :disabled="loading"
          @click="loadSummary(false)"
        >
          重试刷新
        </UiButton>
      </p>

      <UiStatePanel
        class="mt-5"
        :loading="loading && !summary"
        :error="!summary ? errorMessage : ''"
        :empty="Boolean(summary) && filteredModels.length === 0"
        loading-label="渠道状态加载中…"
        skeleton="cards"
        :skeleton-lines="6"
        empty-label="没有匹配的模型"
        empty-hint="调整搜索或模型范围后重试。"
        empty-icon="model"
        error-test-id="model-health-error"
        retry-test-id="model-health-retry"
        @retry="loadSummary(true)"
      >
        <template v-if="summary">
          <div
            v-for="section in groupedModels"
            :key="section.key"
            class="mb-5 last:mb-0"
          >
            <div
              v-if="group !== 'default'"
              class="mb-2 flex items-center gap-2"
            >
              <h2 class="type-heading">
                {{ section.label }}
              </h2>
              <span class="text-xs text-[var(--color-text-subtle)]">{{ section.models.length }}</span>
            </div>
            <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <ModelHealthCard
                v-for="(model, index) in section.models"
                :key="model.model_id"
                :model="model"
                class="stagger-item"
                :style="{ '--stagger-index': index }"
              />
            </div>
          </div>
        </template>
      </UiStatePanel>
    </div>
  </div>
</template>
