<script setup lang="ts">
import { ref } from 'vue'

import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null }>()
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
</script>

<template>
  <div
    data-testid="model-table"
    class="card hidden overflow-hidden md:block"
  >
    <div class="overflow-x-auto">
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
        <tbody>
          <tr
            v-for="model in models"
            :key="model.id"
            class="data-table-row"
          >
            <td class="data-table-td">
              <p class="font-medium text-[var(--color-text)]">
                {{ model.display_name }}
              </p>
              <p class="mt-0.5 font-mono-data text-xs text-[var(--color-text-muted)]">
                {{ model.public_id }}
              </p>
            </td>
            <td class="data-table-td">
              <UiBadge
                variant="info"
                :label="model.kind"
                :dot="false"
              />
            </td>
            <td class="data-table-td">
              <div class="flex flex-wrap gap-x-2 gap-y-1 text-xs">
                <UiBadge
                  :variant="model.supports_vision ? 'success' : 'muted'"
                  :label="`Vision ${model.supports_vision ? '✓' : '—'}`"
                  :dot="false"
                />
                <UiBadge
                  :variant="model.supports_tools ? 'success' : 'muted'"
                  :label="`Tools ${model.supports_tools ? '✓' : '—'}`"
                  :dot="false"
                />
                <UiBadge
                  :variant="model.supports_reasoning ? 'success' : 'muted'"
                  :label="`Reasoning ${model.supports_reasoning ? '✓' : '—'}`"
                  :dot="false"
                />
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
                    class="input-field h-8 w-20 px-2 text-xs"
                    type="number"
                    min="0"
                    step="0.01"
                    data-testid="model-input-price"
                    @input="(e: Event) => { inputDraft = (e.target as HTMLInputElement).value }"
                  >
                  <span class="text-[var(--color-text-muted)]">出</span>
                  <input
                    :value="outputDraft"
                    class="input-field h-8 w-20 px-2 text-xs"
                    type="number"
                    min="0"
                    step="0.01"
                    data-testid="model-output-price"
                    @input="(e: Event) => { outputDraft = (e.target as HTMLInputElement).value }"
                  >
                </div>
                <div class="mt-1.5 flex items-center gap-1.5">
                  <UiButton
                    variant="primary"
                    size="sm"
                    data-testid="model-save-price"
                    :loading="busyId === model.id"
                    loading-label="保存中…"
                    @click="submitPricingEdit(model)"
                  >
                    保存
                  </UiButton>
                  <UiButton
                    variant="ghost"
                    size="sm"
                    @click="cancelPricingEdit"
                  >
                    取消
                  </UiButton>
                </div>
              </div>
              <button
                v-else
                class="rounded-[6px] px-2 py-1 font-mono-data text-xs text-[var(--color-text-secondary)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]"
                type="button"
                data-testid="model-edit-price"
                title="点击编辑单价"
                @click="beginPricingEdit(model)"
              >
                {{ formatPrice(model.input_usd_per_mtok) }} / {{ formatPrice(model.output_usd_per_mtok) }}
              </button>
            </td>
            <td class="data-table-td">
              <span
                class="font-mono-data text-xs"
                :class="model.stream_first_token_timeout_ms !== undefined || model.stream_idle_timeout_ms !== undefined ? 'text-[var(--color-text)]' : 'text-[var(--color-text-muted)]'"
              >
                {{ formatStreamTimeout(model.stream_first_token_timeout_ms, model.stream_idle_timeout_ms) }}
              </span>
            </td>
            <td class="data-table-td">
              <UiBadge
                :variant="model.enabled ? 'success' : 'muted'"
                :label="model.enabled ? '启用' : '停用'"
              />
              <p
                v-if="model.capability_verified_at"
                class="mt-1.5 text-xs text-[var(--color-text-muted)]"
              >
                已验证
              </p>
              <p
                v-else-if="audioNeedsVerification(model)"
                class="mt-1.5 text-xs text-[var(--color-warning)]"
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
            <td class="data-table-td">
              <div class="flex justify-end gap-1.5">
                <UiButton
                  data-testid="model-enable"
                  variant="secondary"
                  size="sm"
                  :disabled="enablingIsBlocked(model) || busyId === model.id"
                  @click="emit('toggle', model)"
                >
                  {{ model.enabled ? '停用' : '启用' }}
                </UiButton>
                <UiButton
                  :data-testid="`model-delete-${model.id}`"
                  variant="danger"
                  size="sm"
                  :disabled="busyId === model.id"
                  @click="emit('delete', model)"
                >
                  删除
                </UiButton>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
