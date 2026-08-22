<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import {
  candidatePublicId,
  candidateSelectionKey,
  capabilityLabels,
  normalizeProvider,
} from './types'
import type { Candidate, Model } from './types'

withDefaults(defineProps<{
  models: Model[]
  candidates?: Candidate[]
  busyId: number | null
  selectedModelIds?: ReadonlySet<number>
  selectedCandidateKeys?: ReadonlySet<string>
}>(), {
  candidates: () => [],
  selectedModelIds: () => new Set<number>(),
  selectedCandidateKeys: () => new Set<string>(),
})
const emit = defineEmits<{
  toggle: [model: Model]
  unblock: [keyId: number, model: Model]
  delete: [model: Model]
  toggleTest: [model: Model, selected: boolean]
  toggleCandidate: [candidate: Candidate, selected: boolean]
  test: [model: Model]
}>()

function audioNeedsVerification(model: Model): boolean {
  return (model.kind === 'asr' || model.kind === 'tts') && !model.capability_verified_at
}

function enablingIsBlocked(model: Model): boolean {
  return !model.enabled && audioNeedsVerification(model)
}

function providerLabel(provider?: string): string {
  return normalizeProvider(provider) === 'opencodefree' ? 'OpenCodeFree' : 'NVIDIA'
}

function reasoningStatusLabel(status?: string): string {
  switch (status) {
    case 'visible': return '可见'
    case 'hidden': return '隐藏'
    case 'inferred': return '推断'
    case 'unsupported': return '不支持'
    default: return '待验证'
  }
}

function reasoningStatusVariant(status?: string): 'success' | 'warning' | 'muted' {
  if (status === 'visible' || status === 'hidden' || status === 'inferred') return 'success'
  if (status === 'unsupported') return 'muted'
  return 'warning'
}

function onCandidateChange(candidate: Candidate, event: globalThis.Event): void {
  emit('toggleCandidate', candidate, (event.target as globalThis.HTMLInputElement).checked)
}

function onModelTestChange(model: Model, event: globalThis.Event): void {
  emit('toggleTest', model, (event.target as globalThis.HTMLInputElement).checked)
}
</script>

<template>
  <div
    data-testid="model-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="candidate in candidates"
      :key="`candidate-${candidateSelectionKey(candidate)}`"
      class="card border-dashed p-4"
    >
      <div class="flex items-start gap-3">
        <input
          :checked="selectedCandidateKeys.has(candidateSelectionKey(candidate))"
          class="mt-0.5 h-4 w-4 rounded border-[var(--color-text-subtle)] bg-[var(--color-sunken)] text-[var(--color-accent)] focus:ring-[color-mix(in_srgb,var(--color-accent)_30%,transparent)]"
          :data-testid="`candidate-${candidateSelectionKey(candidate)}`"
          type="checkbox"
          @change="onCandidateChange(candidate, $event)"
        >
        <div class="min-w-0 flex-1">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h3 class="text-sm font-medium text-[var(--color-text)]">
                {{ candidate.display_name }}
              </h3>
              <p class="mt-0.5 truncate font-mono-data text-xs text-[var(--color-text-muted)]">
                {{ candidatePublicId(candidate) }}
              </p>
            </div>
            <UiBadge
              class="shrink-0"
              :variant="candidate.reasoning_status === 'unknown' ? 'warning' : 'info'"
              :label="providerLabel(candidate.provider)"
            />
          </div>
          <div class="mt-2 flex flex-wrap gap-1.5 text-xs">
            <UiBadge
              variant="info"
              :label="candidate.kind"
              :dot="false"
            />
            <UiBadge
              v-for="label in capabilityLabels(candidate)"
              :key="label"
              variant="success"
              :label="label"
              :dot="false"
            />
            <UiBadge
              v-if="candidate.reasoning_status === 'unknown'"
              variant="warning"
              label="待验证"
              :dot="false"
            />
          </div>
          <p class="mt-2 text-xs text-[var(--color-text-muted)]">
            发现候选 · 保存后保持停用，可参与只读测试
          </p>
        </div>
      </div>
    </article>
    <article
      v-for="model in models"
      :key="model.id"
      class="card p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="flex min-w-0 items-start gap-2">
          <input
            :checked="selectedModelIds.has(model.id)"
            class="mt-0.5 h-4 w-4 shrink-0 rounded border-[var(--color-text-subtle)] bg-[var(--color-sunken)] text-[var(--color-accent)] focus:ring-[color-mix(in_srgb,var(--color-accent)_30%,transparent)]"
            :data-testid="`test-model-card-${model.id}`"
            type="checkbox"
            @change="onModelTestChange(model, $event)"
          >
          <div class="min-w-0">
            <h3 class="text-sm font-medium text-[var(--color-text)]">
              {{ model.display_name }}
            </h3>
            <p class="mt-0.5 truncate font-mono-data text-xs text-[var(--color-text-muted)]">
              {{ model.public_id }}
            </p>
          </div>
        </div>
        <UiBadge
          class="shrink-0"
          :variant="'info'"
          :label="providerLabel(model.provider)"
          :dot="false"
        />
      </div>

      <div class="mt-3 flex flex-wrap gap-1.5 text-xs">
        <UiBadge
          variant="info"
          :label="model.kind"
          :dot="false"
        />
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
          :variant="reasoningStatusVariant(model.reasoning_status)"
          :label="`Reasoning ${model.supports_reasoning ? '✓' : '—'} · ${reasoningStatusLabel(model.reasoning_status)}`"
          :dot="false"
        />
      </div>

      <div class="mt-3 flex items-center justify-between">
        <UiBadge
          :variant="model.enabled ? 'success' : 'muted'"
          :label="model.enabled ? '启用' : '停用'"
        />
        <span class="text-xs text-[var(--color-text-muted)]">勾选后参与只读测试</span>
      </div>

      <p
        v-if="model.capability_verified_at"
        class="mt-2 text-xs text-[var(--color-text-muted)]"
      >
        已验证
      </p>
      <p
        v-else-if="audioNeedsVerification(model)"
        class="mt-2 text-xs text-[var(--color-warning)]"
      >
        需要先完成真实音频能力测试
      </p>

      <div
        v-if="model.blocked_by_key_ids?.length"
        class="mt-3 space-y-1"
      >
        <p class="text-xs text-[var(--color-warning)]">
          已 block：
        </p>
        <button
          v-for="keyId in model.blocked_by_key_ids"
          :key="keyId"
          :data-testid="`model-unblock-${keyId}`"
          class="block text-xs text-[var(--color-danger)] underline disabled:text-[var(--color-text-subtle)]"
          type="button"
          :disabled="busyId === model.id"
          @click="emit('unblock', keyId, model)"
        >
          Key #{{ keyId }} · 手测恢复
        </button>
      </div>

      <div class="mt-4 flex gap-2">
        <UiButton
          :data-testid="`test-model-button-card-${model.id}`"
          variant="ghost"
          size="sm"
          :disabled="busyId === model.id"
          @click="emit('test', model)"
        >
          单测
        </UiButton>
        <UiButton
          data-testid="model-card-toggle"
          variant="secondary"
          size="sm"
          class="flex-1"
          :disabled="enablingIsBlocked(model) || busyId === model.id"
          @click="emit('toggle', model)"
        >
          {{ model.enabled ? '停用' : '启用' }}
        </UiButton>
        <UiButton
          :data-testid="`model-card-delete-${model.id}`"
          variant="danger"
          size="sm"
          :disabled="busyId === model.id"
          @click="emit('delete', model)"
        >
          删除
        </UiButton>
      </div>
    </article>
  </div>
</template>
