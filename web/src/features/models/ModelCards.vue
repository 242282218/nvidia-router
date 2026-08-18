<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null }>()
const emit = defineEmits<{
  toggle: [model: Model]
  unblock: [keyId: number, model: Model]
  delete: [model: Model]
}>()

function audioNeedsVerification(model: Model): boolean {
  return (model.kind === 'asr' || model.kind === 'tts') && !model.capability_verified_at
}

function enablingIsBlocked(model: Model): boolean {
  return !model.enabled && audioNeedsVerification(model)
}
</script>

<template>
  <div
    data-testid="model-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="model in models"
      :key="model.id"
      class="card p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-[var(--color-text)]">
            {{ model.display_name }}
          </h3>
          <p class="mt-0.5 truncate font-mono-data text-xs text-[var(--color-text-muted)]">
            {{ model.public_id }}
          </p>
        </div>
        <UiBadge
          class="shrink-0"
          variant="info"
          :label="model.kind"
          :dot="false"
        />
      </div>

      <div class="mt-3 flex flex-wrap gap-1.5 text-xs">
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

      <div class="mt-3 flex items-center justify-between">
        <UiBadge
          :variant="model.enabled ? 'success' : 'muted'"
          :label="model.enabled ? '启用' : '停用'"
        />
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
