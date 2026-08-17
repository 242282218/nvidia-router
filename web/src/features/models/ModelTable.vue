<script setup lang="ts">
import { ref } from 'vue'

import StatusBadge from '../../shared/components/StatusBadge.vue'
import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null; confirmingId?: number | null }>()
const emit = defineEmits<{
  toggle: [model: Model]
  unblock: [keyId: number, model: Model]
  savePricing: [model: Model, inputUsd: number, outputUsd: number]
  delete: [model: Model]
}>()

// editingPricing tracks the row currently in price-edit mode; raw inputs start
// from the model's stored prices so the operator tweaks rather than retypes.
const editingPrice = ref<number | null>(null)
const inputDraft = ref('')
const outputDraft = ref('')

function beginPricingEdit(model: Model): void {
  editingPrice.value = model.id
  inputDraft.value = model.input_usd_per_mtok !== undefined ? String(model.input_usd_per_mtok) : ''
  outputDraft.value = model.output_usd_per_mtok !== undefined ? String(model.output_usd_per_mtok) : ''
}

function cancelPricingEdit(): void {
  editingPrice.value = null
}

function submitPricingEdit(model: Model): void {
  const input = parsePrice(inputDraft.value)
  const output = parsePrice(outputDraft.value)
  if (input === null || output === null) return
  emit('savePricing', model, input, output)
  editingPrice.value = null
}

// parsePrice: empty means "no price" ($0 contribution), a number must be a
// non-negative USD figure per 1M tokens.
function parsePrice(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return 0
  const value = Number(trimmed)
  if (Number.isNaN(value) || value < 0) return null
  return value
}

function formatPrice(value?: number): string {
  if (value === undefined) return '未定价'
  return `$${value} /1M`
}

// formatStreamTimeout renders the per-model streaming timeout override, or the
// global-default marker when the model carries no override. The columns are
// seeded by migration 016/022 (e.g. deepseek 300s); exposing them here makes the
// override observable without the operator querying the raw API.
function formatStreamTimeout(firstToken?: number, idle?: number): string {
  if (firstToken === undefined && idle === undefined) return '全局默认'
  const parts: string[] = []
  if (firstToken !== undefined) parts.push(`首 ${(firstToken / 1000).toFixed(0)}s`)
  if (idle !== undefined) parts.push(`空闲 ${(idle / 1000).toFixed(0)}s`)
  return parts.join(' · ')
}

function audioNeedsVerification(model: Model): boolean {
  return (model.kind === 'asr' || model.kind === 'tts') && !model.capability_verified_at
}

function enablingIsBlocked(model: Model): boolean {
  return !model.enabled && audioNeedsVerification(model)
}

function capBadge(supported: boolean): string {
  return supported ? 'badge-success' : 'badge-muted'
}
</script>

<template>
  <div
    data-testid="model-table"
    class="hidden overflow-hidden rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] md:block"
  >
    <table class="data-table">
      <caption class="sr-only">
        模型白名单，共 {{ models.length }} 条
      </caption>
      <thead>
        <tr>
          <th
            class="data-table-th"
            scope="col"
          >
            模型
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            Kind
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            能力
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            单价 (USD /1M)
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            流式超时
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            状态
          </th>
          <th
            class="data-table-th text-right"
            scope="col"
          >
            操作
          </th>
        </tr>
      </thead>
      <tbody class="divide-y divide-[var(--color-border)]">
        <tr
          v-for="model in models"
          :key="model.id"
          class="transition-colors hover:bg-[var(--color-hover)]"
        >
          <td class="data-table-td">
            <p class="font-medium text-[var(--color-text)]">
              {{ model.display_name }}
            </p>
            <p class="mt-0.5 font-mono text-xs text-[var(--color-text-muted)]">
              {{ model.public_id }}
            </p>
          </td>
          <td class="data-table-td">
            <span class="badge-info">{{ model.kind }}</span>
          </td>
          <td class="data-table-td">
            <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">
              <span :class="capBadge(model.supports_vision)">Vision {{ model.supports_vision ? '✓' : '—' }}</span>
              <span :class="capBadge(model.supports_tools)">Tools {{ model.supports_tools ? '✓' : '—' }}</span>
              <span :class="capBadge(model.supports_reasoning)">Reasoning {{ model.supports_reasoning ? '✓' : '—' }}</span>
            </div>
          </td>
          <td class="data-table-td">
            <div
              v-if="editingPrice === model.id"
              :data-testid="`model-pricing-edit-${model.id}`"
            >
              <div class="flex items-center gap-1 text-xs">
                <span class="text-[var(--color-text-muted)]">入</span>
                <input
                  :value="inputDraft"
                  class="input-field w-16 px-1.5 py-0.5 text-xs"
                  type="number"
                  min="0"
                  step="0.01"
                  data-testid="model-input-price"
                  @input="(e: Event) => { inputDraft = (e.target as HTMLInputElement).value }"
                >
                <span class="text-[var(--color-text-muted)]">出</span>
                <input
                  :value="outputDraft"
                  class="input-field w-16 px-1.5 py-0.5 text-xs"
                  type="number"
                  min="0"
                  step="0.01"
                  data-testid="model-output-price"
                  @input="(e: Event) => { outputDraft = (e.target as HTMLInputElement).value }"
                >
              </div>
              <div class="mt-1 flex items-center gap-2">
                <button
                  class="btn-primary px-2.5 py-1 text-xs"
                  type="button"
                  data-testid="model-save-price"
                  :disabled="busyId === model.id"
                  @click="submitPricingEdit(model)"
                >
                  {{ busyId === model.id ? '保存中…' : '保存' }}
                </button>
                <button
                  class="btn-ghost px-2.5 py-1 text-xs"
                  type="button"
                  @click="cancelPricingEdit"
                >
                  取消
                </button>
              </div>
            </div>
            <button
              v-else
              class="btn-ghost px-2 py-1 font-mono text-xs"
              type="button"
              data-testid="model-edit-price"
              @click="beginPricingEdit(model)"
            >
              {{ formatPrice(model.input_usd_per_mtok) }} / {{ formatPrice(model.output_usd_per_mtok) }}
            </button>
          </td>
          <td class="data-table-td">
            <span
              class="font-mono text-xs"
              :class="model.stream_first_token_timeout_ms !== undefined || model.stream_idle_timeout_ms !== undefined ? 'text-[var(--color-accent-bright)]' : 'text-[var(--color-text-muted)]'"
            >
              {{ formatStreamTimeout(model.stream_first_token_timeout_ms, model.stream_idle_timeout_ms) }}
            </span>
          </td>
          <td class="data-table-td">
            <StatusBadge
              :variant="model.enabled ? 'success' : 'muted'"
              :label="model.enabled ? '启用' : '停用'"
            />
            <p
              v-if="model.capability_verified_at"
              class="mt-1 text-xs text-[var(--color-text-muted)]"
            >
              已验证
            </p>
            <p
              v-else-if="audioNeedsVerification(model)"
              class="mt-1 text-xs text-[var(--color-warning)]"
            >
              需要先完成真实音频能力测试
            </p>
            <div
              v-if="model.blocked_by_key_ids?.length"
              class="mt-2 space-y-1"
            >
              <p class="text-xs text-[var(--color-warning)]">
                已 block：
              </p>
              <button
                v-for="keyId in model.blocked_by_key_ids"
                :key="keyId"
                :data-testid="`model-table-unblock-${keyId}`"
                class="block text-xs text-[var(--color-danger)] underline hover:opacity-75 disabled:text-[var(--color-text-subtle)]"
                type="button"
                :disabled="busyId === model.id"
                @click="emit('unblock', keyId, model)"
              >
                Key #{{ keyId }} · 手测恢复
              </button>
            </div>
          </td>
          <td class="data-table-td text-right">
            <div class="flex justify-end gap-1.5">
              <button
                data-testid="model-enable"
                class="btn-secondary"
                type="button"
                :disabled="enablingIsBlocked(model) || busyId === model.id"
                @click="emit('toggle', model)"
              >
                {{ model.enabled ? '停用' : '启用' }}
              </button>
              <button
                :data-testid="`model-delete-${model.id}`"
                class="btn-danger"
                type="button"
                :disabled="busyId === model.id"
                @click="emit('delete', model)"
              >
                {{ confirmingId === model.id ? '确认删除？' : '删除' }}
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="models.length === 0">
          <td
            colspan="7"
            class="px-4 py-8 text-center text-[var(--color-text-muted)]"
          >
            暂无模型白名单。
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>