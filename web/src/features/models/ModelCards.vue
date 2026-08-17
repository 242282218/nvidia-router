<script setup lang="ts">
import StatusBadge from '../../shared/components/StatusBadge.vue'
import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null; confirmingId?: number | null }>()
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

function capBadge(supported: boolean): string {
  return supported ? 'badge-success' : 'badge-muted'
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
      class="card-hover p-4 animate-slide-up"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-[var(--color-text)]">
            {{ model.display_name }}
          </h3>
          <p class="mt-0.5 truncate font-mono text-xs text-[var(--color-text-muted)]">
            {{ model.public_id }}
          </p>
        </div>
        <span class="badge-info shrink-0">{{ model.kind }}</span>
      </div>

      <div class="mt-3 flex flex-wrap gap-2 text-xs">
        <span :class="capBadge(model.supports_vision)">Vision {{ model.supports_vision ? '✓' : '—' }}</span>
        <span :class="capBadge(model.supports_tools)">Tools {{ model.supports_tools ? '✓' : '—' }}</span>
        <span :class="capBadge(model.supports_reasoning)">Reasoning {{ model.supports_reasoning ? '✓' : '—' }}</span>
      </div>

      <div class="mt-3 flex items-center justify-between">
        <StatusBadge
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
        <button
          data-testid="model-card-toggle"
          class="btn-secondary flex-1"
          type="button"
          :disabled="enablingIsBlocked(model) || busyId === model.id"
          @click="emit('toggle', model)"
        >
          {{ model.enabled ? '停用' : '启用' }}
        </button>
        <button
          :data-testid="`model-card-delete-${model.id}`"
          class="btn-danger"
          type="button"
          :disabled="busyId === model.id"
          @click="emit('delete', model)"
        >
          {{ confirmingId === model.id ? '确认删除？' : '删除' }}
        </button>
      </div>
    </article>
    <p
      v-if="models.length === 0"
      class="rounded-[var(--radius-panel)] border border-dashed border-[var(--color-border)] p-6 text-center text-sm text-[var(--color-text-muted)]"
    >
      暂无模型白名单。
    </p>
  </div>
</template>