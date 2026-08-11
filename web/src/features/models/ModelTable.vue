<script setup lang="ts">
import { ref } from 'vue'

import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null }>()
const emit = defineEmits<{
  toggle: [model: Model]
  unblock: [keyId: number, model: Model]
  savePricing: [model: Model, inputUsd: number, outputUsd: number]
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
    class="hidden overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] md:block"
  >
    <table class="data-table">
      <thead>
        <tr>
          <th class="data-table-th">
            模型
          </th>
          <th class="data-table-th">
            Kind
          </th>
          <th class="data-table-th">
            能力
          </th>
          <th class="data-table-th">
            单价 (USD /1M)
          </th>
          <th class="data-table-th">
            状态
          </th>
          <th class="data-table-th text-right">
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
                  class="text-xs font-medium text-[var(--color-accent)] disabled:opacity-40"
                  type="button"
                  data-testid="model-save-price"
                  :disabled="busyId === model.id"
                  @click="submitPricingEdit(model)"
                >
                  保存
                </button>
                <button
                  class="text-xs text-[var(--color-text-muted)]"
                  type="button"
                  @click="cancelPricingEdit"
                >
                  取消
                </button>
              </div>
            </div>
            <button
              v-else
              class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-accent)]"
              type="button"
              data-testid="model-edit-price"
              @click="beginPricingEdit(model)"
            >
              <span class="font-mono">{{ formatPrice(model.input_usd_per_mtok) }} / {{ formatPrice(model.output_usd_per_mtok) }}</span>
            </button>
          </td>
          <td class="data-table-td">
            <span
              v-if="model.enabled"
              class="badge-success"
            >启用</span>
            <span
              v-else
              class="badge-muted"
            >停用</span>
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
                class="block text-xs text-[#F87171] underline hover:text-[#FCA5A5] disabled:opacity-40"
                type="button"
                :disabled="busyId === model.id"
                @click="emit('unblock', keyId, model)"
              >
                Key #{{ keyId }} · 手测恢复
              </button>
            </div>
          </td>
          <td class="data-table-td text-right">
            <button
              data-testid="model-enable"
              class="btn-secondary rounded-md px-3 py-1 text-xs"
              type="button"
              :disabled="enablingIsBlocked(model) || busyId === model.id"
              @click="emit('toggle', model)"
            >
              {{ model.enabled ? '停用' : '启用' }}
            </button>
          </td>
        </tr>
        <tr v-if="models.length === 0">
          <td
            colspan="6"
            class="px-4 py-8 text-center text-[var(--color-text-muted)]"
          >
            暂无模型白名单。
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>