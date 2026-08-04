<script setup lang="ts">
import type { KeyTestResult } from './types'

defineProps<{ open: boolean; results: KeyTestResult[] }>()
const emit = defineEmits<{ close: [] }>()

function statusBadge(status: string): string {
  switch (status) {
    case 'ok': return 'badge-success'
    case 'error': return 'badge-danger'
    default: return 'badge-warning'
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
      @keydown.esc="emit('close')"
    >
      <section
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
              <span :class="statusBadge(result.status)">{{ result.status }}</span>
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
.modal-enter-active,
.modal-leave-active {
  transition: all 0.2s ease;
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