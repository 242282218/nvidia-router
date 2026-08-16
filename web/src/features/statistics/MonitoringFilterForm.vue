<script setup lang="ts">
import { reactive, ref } from 'vue'

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
    class="card grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4"
    @submit.prevent="submit"
  >
    <label class="sm:col-span-2">
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">关键词</span>
      <input
        v-model="fields.search"
        data-testid="monitoring-search"
        class="input-field mt-1"
        type="search"
        maxlength="128"
        placeholder="请求 ID、模型、接口、错误码"
      >
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">模型</span>
      <input
        v-model="fields.model_id"
        class="input-field mt-1"
        type="text"
        maxlength="128"
        placeholder="全部模型"
      >
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">接口</span>
      <input
        v-model="fields.endpoint"
        class="input-field mt-1"
        type="text"
        maxlength="128"
        placeholder="全部接口"
      >
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">结果状态</span>
      <select
        v-model="fields.outcome"
        data-testid="monitoring-status"
        class="input-field mt-1"
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
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">HTTP 状态码</span>
      <input
        v-model="fields.status"
        data-testid="monitoring-status-code"
        class="input-field mt-1"
        type="number"
        min="100"
        max="599"
        placeholder="全部"
      >
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">Access Key ID</span>
      <input
        v-model="fields.access_key_id"
        class="input-field mt-1"
        type="number"
        min="1"
        placeholder="全部"
      >
    </label>
    <label>
      <span class="text-xs font-medium text-[var(--color-text-secondary)]">NVIDIA Key ID</span>
      <input
        v-model="fields.nvidia_key_id"
        class="input-field mt-1"
        type="number"
        min="1"
        placeholder="全部"
      >
    </label>
    <div class="flex items-end justify-end sm:col-span-2 lg:col-span-4">
      <button
        class="btn-primary"
        type="submit"
      >
        应用筛选
      </button>
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
