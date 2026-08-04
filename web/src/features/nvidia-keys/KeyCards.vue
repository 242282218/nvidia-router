<script setup lang="ts">
import type { NVIDIAKey } from './types'

defineProps<{ keys: NVIDIAKey[]; busyId: number | null }>()
const emit = defineEmits<{
  toggle: [key: NVIDIAKey]
  test: [key: NVIDIAKey]
  remove: [key: NVIDIAKey]
}>()

function statusLabel(key: NVIDIAKey): string {
  if (key.auth_invalid) return '认证失效'
  if (key.cooldown_until) return key.cooldown_reason || '冷却中'
  return key.enabled ? '启用' : '停用'
}

function statusBadgeClass(key: NVIDIAKey): string {
  if (key.auth_invalid) return 'badge-danger'
  if (key.cooldown_until) return 'badge-warning'
  return key.enabled ? 'badge-success' : 'badge-muted'
}

function formatDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}
</script>

<template>
  <div
    data-testid="key-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="key in keys"
      :key="key.id"
      class="card-hover p-4 animate-slide-up"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <code class="block truncate font-mono text-sm text-[var(--color-info)]">{{ key.masked }}</code>
          <span
            :class="statusBadgeClass(key)"
            class="mt-2 inline-flex"
          >{{ statusLabel(key) }}</span>
        </div>
        <span class="shrink-0 font-mono text-xs text-[var(--color-text-muted)]">#{{ key.id }}</span>
      </div>

      <div class="mt-4 grid grid-cols-2 gap-2 text-xs">
        <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-sunken)] p-3">
          <p class="text-[var(--color-text-muted)]">
            连续失败
          </p>
          <p class="mt-1 font-mono text-sm text-[var(--color-text-secondary)]">
            {{ key.consecutive_failures }}
          </p>
        </div>
        <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-sunken)] p-3">
          <p class="text-[var(--color-text-muted)]">
            最近错误
          </p>
          <p class="mt-1 truncate font-mono text-sm text-[var(--color-danger)]">
            {{ key.last_error_code || '—' }}
          </p>
        </div>
      </div>

      <dl class="mt-3 space-y-1.5 text-xs text-[var(--color-text-muted)]">
        <div
          v-if="key.cooldown_until"
          class="flex justify-between gap-3"
        >
          <dt>冷却至</dt>
          <dd class="font-mono text-right">
            {{ formatDate(key.cooldown_until) }}
            <span class="sr-only">{{ key.cooldown_until }}</span>
          </dd>
        </div>
        <div
          v-if="key.last_error_at"
          class="flex justify-between gap-3"
        >
          <dt>最近错误</dt>
          <dd class="font-mono text-right">
            {{ formatDate(key.last_error_at) }}
            <span class="sr-only">{{ key.last_error_at }}</span>
          </dd>
        </div>
      </dl>

      <div class="mt-4 grid grid-cols-3 gap-2">
        <button
          data-testid="key-card-toggle"
          class="btn-secondary min-h-11 rounded-lg py-2 text-xs"
          type="button"
          :disabled="busyId === key.id"
          @click="emit('toggle', key)"
        >
          {{ key.enabled ? '停用' : '启用' }}
        </button>
        <button
          data-testid="key-card-test"
          class="btn-secondary min-h-11 rounded-lg py-2 text-xs"
          type="button"
          :disabled="busyId === key.id"
          @click="emit('test', key)"
        >
          单测
        </button>
        <button
          data-testid="key-card-delete"
          class="btn-danger min-h-11 rounded-lg py-2 text-xs"
          type="button"
          :disabled="busyId === key.id"
          @click="emit('remove', key)"
        >
          删除
        </button>
      </div>
    </article>
    <p
      v-if="keys.length === 0"
      class="rounded-xl border border-dashed border-[var(--color-border)] p-6 text-center text-sm text-[var(--color-text-muted)]"
    >
      暂无 NVIDIA Key。
    </p>
  </div>
</template>
