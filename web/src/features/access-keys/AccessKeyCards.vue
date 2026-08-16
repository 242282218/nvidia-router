<script setup lang="ts">
import StatusBadge from '../../shared/components/StatusBadge.vue'
import { budgetUsagePercent, formatKeyValue, formatTokens, keyState } from './state'
import type { AccessKey } from './types'

defineProps<{ keys: AccessKey[]; busyId: number | null; confirmingId: number | null }>()

const emit = defineEmits<{
  edit: [key: AccessKey]
  revoke: [key: AccessKey]
}>()
</script>

<template>
  <div
    data-testid="access-key-cards"
    class="space-y-2 p-4 md:hidden"
  >
    <article
      v-for="key in keys"
      :key="`card-${key.id}`"
      class="card-hover animate-slide-up p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-[var(--color-text)]">
            {{ key.name }}
          </h3>
          <code class="mt-1 block truncate font-mono text-xs text-[var(--color-info)]">{{ key.key_prefix }}</code>
        </div>
        <StatusBadge
          class="shrink-0"
          :variant="keyState(key).variant"
          :label="keyState(key).label"
        />
      </div>
      <div class="mt-3 space-y-1 text-xs text-[var(--color-text-muted)]">
        <div class="flex justify-between">
          <span>创建时间</span>
          <span>{{ formatKeyValue(key.created_at) }}</span>
        </div>
        <div class="flex justify-between">
          <span>最后使用</span>
          <span>{{ formatKeyValue(key.last_used_at) }}</span>
        </div>
        <div
          v-if="key.expires_at"
          class="flex justify-between"
        >
          <span>过期时间</span>
          <span>{{ formatKeyValue(key.expires_at) }}</span>
        </div>
        <div
          v-if="key.token_budget > 0"
          class="flex justify-between"
        >
          <span>Token 预算</span>
          <span>{{ formatTokens(key.consumed_tokens) }} / {{ formatTokens(key.token_budget) }}（{{ budgetUsagePercent(key) }}%）</span>
        </div>
      </div>
      <div class="mt-4 flex gap-2">
        <button
          :data-testid="`mobile-edit-access-key-policy-${key.id}`"
          class="btn-secondary flex-1"
          type="button"
          :disabled="Boolean(key.revoked_at)"
          @click="emit('edit', key)"
        >
          编辑策略
        </button>
        <button
          :data-testid="`mobile-revoke-access-key-${key.id}`"
          class="btn-danger flex-1"
          type="button"
          :disabled="Boolean(key.revoked_at) || busyId === key.id"
          @click="emit('revoke', key)"
        >
          {{ confirmingId === key.id ? '确认撤销？' : '撤销' }}
        </button>
      </div>
    </article>
  </div>
</template>
