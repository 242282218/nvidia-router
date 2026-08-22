<script setup lang="ts">
import { reactive, ref } from 'vue'

import UiButton from '../../shared/ui/UiButton.vue'
import type { MonitoringFilter, MonitoringRange } from './types'

// 两行筛选面板（设计 §5.1，承接原 MonitoringFilterForm 的字段与校验逻辑）：
// 行 1 = 时间范围 segmented + 关键词搜索 + 应用筛选 / 刷新 / 清空；
// 行 2 = 六个维度筛选等宽排布。草稿输入在组件内，只有显式提交才生效。
defineOptions({ name: 'MonitoringFilterPanel' })

defineProps<{
  range: MonitoringRange
  loading?: boolean
}>()

const emit = defineEmits<{
  'update:range': [range: MonitoringRange]
  apply: [filters: MonitoringFilter]
  reset: []
  refresh: []
}>()

const rangeOptions: Array<{ value: MonitoringRange; label: string }> = [
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
]

const fields = reactive({
  search: '',
  model_id: '',
  endpoint: '',
  outcome: '',
  status: '',
  access_key_id: '',
  nvidia_key_id: '',
})

// Validation errors stay next to the field group that produced them; an
// invalid filter must not silently vanish nor trigger a reload.
const error = ref('')

// clearFields empties every draft input from the parent (e.g. the "清除筛选"
// action on an empty filtered table) without emitting a filter change; the
// caller decides whether to reload afterwards.
function clearFields(): void {
  for (const key of Object.keys(fields) as Array<keyof typeof fields>) {
    fields[key] = ''
  }
  error.value = ''
}

function resetFilters(): void {
  clearFields()
  emit('reset')
}

function selectRange(next: MonitoringRange): void {
  emit('update:range', next)
}

function submit(): void {
  const { filters, error: validationError } = collectFilters()
  if (validationError) {
    error.value = validationError
    return
  }
  error.value = ''
  emit('apply', filters)
}

function collectFilters(): { filters: MonitoringFilter; error?: string } {
  const filters: MonitoringFilter = {}
  addTextFilter(filters, 'search', fields.search)
  addTextFilter(filters, 'model_id', fields.model_id)
  addTextFilter(filters, 'endpoint', fields.endpoint)
  if (fields.outcome === 'success' || fields.outcome === 'failure') filters.outcome = fields.outcome
  const status = parsePositiveInteger(fields.status)
  const accessKeyID = parsePositiveInteger(fields.access_key_id)
  const nvidiaKeyID = parsePositiveInteger(fields.nvidia_key_id)
  // Vue coerces type=number inputs to numbers at runtime; normalize before
  // the emptiness checks below (the old .trim() call crashed on numbers).
  if (isNonEmptyNumeric(fields.status) && status === undefined) {
    return { filters, error: 'HTTP 状态码必须是正整数（100-599）。' }
  }
  if (isNonEmptyNumeric(fields.access_key_id) && accessKeyID === undefined) {
    return { filters, error: 'Access Key ID 必须是正整数。' }
  }
  if (isNonEmptyNumeric(fields.nvidia_key_id) && nvidiaKeyID === undefined) {
    return { filters, error: 'NVIDIA Key ID 必须是正整数。' }
  }
  if (status !== undefined) filters.status = status
  if (accessKeyID !== undefined) filters.access_key_id = accessKeyID
  if (nvidiaKeyID !== undefined) filters.nvidia_key_id = nvidiaKeyID
  return { filters }
}

function addTextFilter(filters: MonitoringFilter, key: 'search' | 'model_id' | 'endpoint', value: string): void {
  const trimmed = value.trim()
  if (trimmed) filters[key] = trimmed
}

function parsePositiveInteger(value: string | number): number | undefined {
  const text = String(value).trim()
  if (!text) return undefined
  const parsed = Number(text)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function isNonEmptyNumeric(value: string | number): boolean {
  return String(value).trim() !== ''
}

// Cross-card drilldown (FailureFeed → 按模型过滤): seed a draft field from the
// parent. Only text dimensions are supported; the caller still triggers the
// reload after setting appliedFilters, mirroring a manual apply.
function setDraftField(key: 'model_id' | 'endpoint' | 'search', value: string): void {
  fields[key] = value
  error.value = ''
}

defineExpose({ clearFields, setDraftField })
</script>

<template>
  <form
    data-testid="monitoring-filters"
    class="card p-4"
    @submit.prevent="submit"
  >
    <!-- 行 1：时间范围 + 搜索 + 主操作 -->
    <div class="flex flex-wrap items-center gap-2">
      <div
        class="segment-group"
        role="group"
        aria-label="监控时间范围"
      >
        <button
          v-for="option in rangeOptions"
          :key="option.value"
          :data-testid="`range-${option.value}`"
          class="segment-item"
          :class="range === option.value ? 'segment-item-active' : 'segment-item-idle'"
          type="button"
          :aria-pressed="range === option.value"
          @click="selectRange(option.value)"
        >
          {{ option.label }}
        </button>
      </div>
      <div class="relative min-w-[220px] flex-1">
        <label
          class="sr-only"
          for="monitoring-search-input"
        >关键词搜索</label>
        <input
          id="monitoring-search-input"
          v-model="fields.search"
          data-testid="monitoring-search"
          class="input-field pr-9"
          type="search"
          maxlength="128"
          placeholder="请求 ID、模型、接口、错误码"
        >
      </div>
      <UiButton
        variant="primary"
        type="submit"
        icon="filter"
        data-testid="monitoring-filter-apply"
      >
        应用筛选
      </UiButton>
      <UiButton
        variant="ghost"
        type="button"
        icon="refresh"
        :loading="loading"
        loading-label="刷新中…"
        data-testid="monitoring-refresh"
        @click="emit('refresh')"
      >
        刷新
      </UiButton>
      <UiButton
        variant="ghost"
        type="button"
        data-testid="monitoring-filter-reset"
        @click="resetFilters"
      >
        清空
      </UiButton>
    </div>

    <!-- 行 2：六个维度筛选 -->
    <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-6">
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-model-input"
        >模型</label>
        <input
          id="monitoring-model-input"
          v-model="fields.model_id"
          data-testid="monitoring-model-input"
          class="input-field"
          type="text"
          maxlength="128"
          placeholder="全部模型"
        >
      </div>
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-endpoint-input"
        >接口</label>
        <input
          id="monitoring-endpoint-input"
          v-model="fields.endpoint"
          class="input-field"
          type="text"
          maxlength="128"
          placeholder="全部接口"
        >
      </div>
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-access-key-input"
        >Access Key ID</label>
        <input
          id="monitoring-access-key-input"
          v-model="fields.access_key_id"
          class="input-field"
          type="number"
          min="1"
          placeholder="全部"
        >
      </div>
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-nvidia-key-input"
        >NVIDIA Key ID</label>
        <input
          id="monitoring-nvidia-key-input"
          v-model="fields.nvidia_key_id"
          class="input-field"
          type="number"
          min="1"
          placeholder="全部"
        >
      </div>
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-status-select"
        >结果状态</label>
        <select
          id="monitoring-status-select"
          v-model="fields.outcome"
          data-testid="monitoring-status"
          class="input-field"
        >
          <option value="">
            全部状态
          </option>
          <option value="success">
            成功
          </option>
          <option value="failure">
            失败
          </option>
        </select>
      </div>
      <div>
        <label
          class="field-label text-xs"
          for="monitoring-status-code-input"
        >HTTP 状态码</label>
        <input
          id="monitoring-status-code-input"
          v-model="fields.status"
          data-testid="monitoring-status-code"
          class="input-field"
          type="number"
          min="100"
          max="599"
          placeholder="全部"
        >
      </div>
    </div>

    <p
      v-if="error"
      data-testid="monitoring-filter-error"
      class="mt-2 text-sm text-[var(--color-danger)]"
      role="alert"
    >
      {{ error }}
    </p>
  </form>
</template>
