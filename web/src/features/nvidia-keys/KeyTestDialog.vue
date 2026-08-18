<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import type { KeyTestResult } from './types'

defineProps<{ open: boolean; results: KeyTestResult[] }>()

const emit = defineEmits<{ close: [] }>()

function statusVariant(status: string): 'success' | 'danger' | 'warning' {
  switch (status) {
    // The backend reports a successful live test as "valid" (not "ok").
    case 'ok':
    case 'valid': return 'success'
    case 'error': return 'danger'
    default: return 'warning'
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'ok':
    case 'valid': return '可用'
    case 'error': return '失败'
    default: return status
  }
}
</script>

<template>
  <UiModal
    :open="open && results.length > 0"
    title="NVIDIA Key 测试结果"
    size="sm"
    @close="emit('close')"
  >
    <div
      class="space-y-3"
      data-testid="key-test-results"
    >
      <article
        v-for="result in results"
        :key="result.id"
        class="panel-inset p-4"
      >
        <div class="mb-2.5 flex items-center justify-between">
          <span class="text-sm font-medium text-[var(--color-text)]">Key #{{ result.id }}</span>
          <UiBadge
            :variant="statusVariant(result.status)"
            :label="statusLabel(result.status)"
            :dot="false"
          />
        </div>
        <dl class="space-y-2 text-sm">
          <div
            v-if="result.reason"
            class="flex justify-between gap-4"
          >
            <dt class="text-[var(--color-text-muted)]">
              原因
            </dt>
            <dd class="text-right text-[var(--color-text-secondary)]">
              {{ result.reason }}
            </dd>
          </div>
          <div
            v-if="result.request_id"
            class="flex justify-between gap-4"
          >
            <dt class="text-[var(--color-text-muted)]">
              Request ID
            </dt>
            <dd class="font-mono-data text-right text-xs text-[var(--color-info)]">
              {{ result.request_id }}
            </dd>
          </div>
          <div
            v-if="result.models?.length"
            class="flex justify-between gap-4"
          >
            <dt class="text-[var(--color-text-muted)]">
              模型数
            </dt>
            <dd class="text-[var(--color-text-secondary)]">
              {{ result.models.length }}
            </dd>
          </div>
        </dl>
      </article>
    </div>
  </UiModal>
</template>
