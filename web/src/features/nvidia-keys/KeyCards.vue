<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import { formatDate, keyState } from './state'
import type { NVIDIAKey } from './types'

defineProps<{ keys: NVIDIAKey[]; busyId: number | null }>()

const emit = defineEmits<{
  toggle: [key: NVIDIAKey]
  test: [key: NVIDIAKey]
  remove: [key: NVIDIAKey]
}>()
</script>

<template>
  <div
    data-testid="key-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="key in keys"
      :key="key.id"
      class="card p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <code class="block truncate font-mono-data text-sm text-[var(--color-info)]">{{ key.masked }}</code>
          <UiBadge
            class="mt-2"
            :variant="keyState(key).variant"
            :label="keyState(key).label"
          />
        </div>
        <span class="shrink-0 font-mono-data text-xs text-[var(--color-text-muted)]">#{{ key.id }}</span>
      </div>

      <div class="mt-4 grid grid-cols-2 gap-2 text-xs">
        <div class="panel-inset p-3">
          <p class="text-[var(--color-text-muted)]">
            连续失败
          </p>
          <p class="mt-1 font-mono-data text-sm text-[var(--color-text-secondary)]">
            {{ key.consecutive_failures }}
          </p>
        </div>
        <div class="panel-inset p-3">
          <p class="text-[var(--color-text-muted)]">
            最近错误
          </p>
          <p class="mt-1 truncate font-mono-data text-sm text-[var(--color-danger)]">
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
          <dd class="font-mono-data text-right">
            {{ formatDate(key.cooldown_until) }}
            <span class="sr-only">{{ key.cooldown_until }}</span>
          </dd>
        </div>
        <div
          v-if="key.last_error_at"
          class="flex justify-between gap-3"
        >
          <dt>最近错误</dt>
          <dd class="font-mono-data text-right">
            {{ formatDate(key.last_error_at) }}
            <span class="sr-only">{{ key.last_error_at }}</span>
          </dd>
        </div>
      </dl>

      <div class="mt-4 grid grid-cols-3 gap-2">
        <UiButton
          data-testid="key-card-toggle"
          variant="secondary"
          size="sm"
          :disabled="busyId === key.id"
          @click="emit('toggle', key)"
        >
          {{ key.enabled ? '停用' : '启用' }}
        </UiButton>
        <UiButton
          data-testid="key-card-test"
          variant="secondary"
          size="sm"
          :disabled="busyId === key.id"
          @click="emit('test', key)"
        >
          单测
        </UiButton>
        <UiButton
          data-testid="key-card-delete"
          variant="danger"
          size="sm"
          :disabled="busyId === key.id"
          @click="emit('remove', key)"
        >
          删除
        </UiButton>
      </div>
    </article>
  </div>
</template>
