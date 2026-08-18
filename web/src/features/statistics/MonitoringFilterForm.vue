<script setup lang="ts">
import { reactive, ref } from 'vue'

import UiButton from '../../shared/ui/UiButton.vue'
import type { MonitoringFilter } from './types'

// Draft filter inputs live here so switching range or reloading never wipes
// what the operator has typed; only an explicit submit emits the filters.
defineOptions({ name: 'MonitoringFilterForm' })

const emit = defineEmits<{ apply: [filters: MonitoringFilter] }>()

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
</script>

<template>
  <form
    data-testid="monitoring-filters"
    class="card grid gap-x-4 gap-y-3 p-5 sm:grid-cols-2 lg:grid-cols-4"
    @submit.prevent="submit"
  >
    <div class="sm:col-span-2">
      <label
        class="field-label text-xs"
        for="monitoring-search-input"
      >关键词</label>
      <input
        id="monitoring-search-input"
        v-model="fields.search"
        data-testid="monitoring-search"
        class="input-field"
        type="search"
        maxlength="128"
        placeholder="请求 ID、模型、接口、错误码"
      >
    </div>
    <div>
      <label
        class="field-label text-xs"
        for="monitoring-model-input"
      >模型</label>
      <input
        id="monitoring-model-input"
        v-model="fields.model_id"
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
    <div class="flex items-end justify-end sm:col-span-2 lg:col-span-4">
      <UiButton
        variant="primary"
        type="submit"
        icon="filter"
      >
        应用筛选
      </UiButton>
    </div>
    <p
      v-if="error"
      data-testid="monitoring-filter-error"
      class="text-sm text-[var(--color-danger)] sm:col-span-2 lg:col-span-4"
      role="alert"
    >
      {{ error }}
    </p>
  </form>
</template>
