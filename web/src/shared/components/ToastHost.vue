<script setup lang="ts">
import { toastState, dismiss, pauseDismiss, resumeDismiss, type ToastType } from '../toast'

defineOptions({ name: 'ToastHost' })

const accentByType: Record<ToastType, string> = {
  success: 'var(--color-success)',
  error: 'var(--color-danger)',
  info: 'var(--color-info)',
  warning: 'var(--color-warning)',
}

// Errors announce assertively, successes politely (design-aesthetics toast:
// role="alert" for errors, role="status" for the rest). A live region must not
// be dynamically inserted, so the container is always mounted.
function toastRole(type: ToastType): 'alert' | 'status' {
  return type === 'error' ? 'alert' : 'status'
}

function onToastEnter(id: number): void {
  // Pause the auto-dismiss so a hovered/focused toast stays readable; resume the
  // full window on leave (not continue) so the user re-reads without a timer
  // racing the pointer.
  pauseDismiss(id)
}

function onToastLeave(id: number, type: ToastType): void {
  resumeDismiss(id, type)
}
</script>

<template>
  <div
    class="pointer-events-none fixed bottom-[calc(1rem+env(safe-area-inset-bottom))] right-4 z-[var(--z-toast)] flex w-[min(20rem,calc(100vw-2rem))] max-w-[calc(100vw-2rem)] min-w-0 flex-col gap-2 md:right-6"
    aria-live="polite"
    aria-atomic="false"
  >
    <TransitionGroup name="toast">
      <div
        v-for="toast in toastState.toasts"
        :key="toast.id"
        :role="toastRole(toast.type)"
        class="pointer-events-auto flex items-start gap-3 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-elevated)] px-3.5 py-3"
        @mouseenter="onToastEnter(toast.id)"
        @mouseleave="onToastLeave(toast.id, toast.type)"
        @focusin="onToastEnter(toast.id)"
        @focusout="onToastLeave(toast.id, toast.type)"
      >
        <svg
          class="mt-0.5 h-4 w-4 shrink-0"
          :style="{ color: accentByType[toast.type] }"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            v-if="toast.type === 'success'"
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4.5 12.75l6 6 9-13.5"
          />
          <path
            v-else-if="toast.type === 'warning'"
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"
          />
          <path
            v-else
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M12 9v3.75m0 3.75h.008v.008H12v-.008zM9 3.75H4.5a1.5 1.5 0 00-1.5 1.5v13.5a1.5 1.5 0 001.5 1.5h15a1.5 1.5 0 001.5-1.5V5.25a1.5 1.5 0 00-1.5-1.5H15M9 3.75a2.25 2.25 0 014.5 0v1.5a.75.75 0 01-.75.75h-3a.75.75 0 01-.75-.75v-1.5z"
          />
        </svg>
        <p class="min-w-0 flex-1 text-sm leading-snug text-[var(--color-text)]">
          {{ toast.message }}
        </p>
        <button
          class="flex h-11 w-11 shrink-0 items-center justify-center rounded text-[var(--color-text-subtle)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:bg-[var(--color-active)]"
          type="button"
          aria-label="关闭提示"
          @click="dismiss(toast.id)"
        >
          <svg
            class="h-4 w-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
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
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.18s cubic-bezier(0.2, 0, 0, 1), transform 0.18s cubic-bezier(0.2, 0, 0, 1);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(0.5rem);
}
</style>
