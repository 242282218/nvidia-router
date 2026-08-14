<script setup lang="ts">
import { computed, ref } from 'vue'

import { useDialog } from '../../shared/useDialog'
import type { KeyTestResult } from './types'

const props = defineProps<{ open: boolean; results: KeyTestResult[] }>()
const emit = defineEmits<{ close: [] }>()

const panel = ref<globalThis.HTMLElement | null>(null)
useDialog(computed(() => props.open), panel, () => emit('close'))

function statusBadge(status: string): string {
  switch (status) {
    // The backend reports a successful live test as "valid" (not "ok").
    case 'ok':
    case 'valid': return 'badge-success'
    case 'error': return 'badge-danger'
    default: return 'badge-warning'
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
  <Transition name="modal">
    <div
      v-if="open && results.length"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      aria-labelledby="key-test-results-title"
      @click.self="emit('close')"
    >
      <section
        ref="panel"
        data-testid="key-test-results"
        class="modal-panel max-h-[85vh] max-w-lg overflow-y-auto"
      >
        <!-- Header -->
        <div class="flex items-center justify-between border-b border-[var(--color-border)] px-6 py-4">
          <h2
            id="key-test-results-title"
            class="text-base font-semibold text-[var(--color-text)]"
          >
            NVIDIA Key 测试结果
          </h2>
          <button
            class="btn-ghost rounded-lg p-2"
            type="button"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg
              class="h-5 w-5"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        </div>

        <!-- Results -->
        <div class="p-6 space-y-4">
          <article
            v-for="result in results"
            :key="result.id"
            class="rounded-lg border border-[var(--color-border)] bg-[var(--color-sunken)]/40 p-4"
          >
            <div class="flex items-center justify-between mb-3">
              <span class="text-sm font-medium text-[var(--color-text)]">Key #{{ result.id }}</span>
              <span :class="statusBadge(result.status)">{{ statusLabel(result.status) }}</span>
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
                <dd class="font-mono text-right text-xs text-[var(--color-info)]">
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
                <dd>{{ result.models.length }}</dd>
              </div>
            </dl>
          </article>
        </div>
      </section>
    </div>
  </Transition>
</template>

<style scoped>
.modal-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.modal-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from section,
.modal-leave-to section {
  transform: scale(0.95);
}
</style>