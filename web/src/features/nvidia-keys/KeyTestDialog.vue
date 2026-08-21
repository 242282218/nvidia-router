<script setup lang="ts">
import { computed } from 'vue'

import UiBadge from '../../shared/ui/UiBadge.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import type { KeyTestResult } from './types'

const props = defineProps<{
  open: boolean
  results: KeyTestResult[]
  /** id → masked value from the parent's loaded key list; results render
   * "Key #id" when a mask is missing (e.g. the key was deleted mid-run). */
  maskedById?: Map<number, string>
}>()

const emit = defineEmits<{ close: [] }>()

function resultLabel(result: KeyTestResult): string {
  const masked = props.maskedById?.get(result.id)
  return masked ? `${masked}（#${result.id}）` : `Key #${result.id}`
}

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

const ordered = computed(() => [...props.results].sort((a, b) => {
  const rank = (status: string): number => (status === 'ok' || status === 'valid' ? 0 : 1)
  return rank(a.status) - rank(b.status)
}))
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
        v-for="result in ordered"
        :key="result.id"
        class="panel-inset p-4"
      >
        <div class="mb-2.5 flex items-center justify-between">
          <span class="font-mono-data text-sm font-medium text-[var(--color-text)]">{{ resultLabel(result) }}</span>
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
