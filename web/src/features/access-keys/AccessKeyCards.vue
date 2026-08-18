<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import { budgetUsagePercent, formatKeyValue, formatTokens, keyState } from './state'
import type { AccessKey } from './types'

defineProps<{
  keys: AccessKey[]
  busyId: number | null
}>()

const emit = defineEmits<{
  edit: [key: AccessKey]
  revoke: [key: AccessKey]
  delete: [key: AccessKey]
}>()
</script>

<template>
  <div
    data-testid="access-key-cards"
    class="space-y-3 p-4 md:hidden"
  >
    <article
      v-for="key in keys"
      :key="`card-${key.id}`"
      class="panel-inset p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-[var(--color-text)]">
            {{ key.name }}
          </h3>
          <code class="mt-1 block truncate font-mono-data text-xs text-[var(--color-info)]">{{ key.key_prefix }}</code>
        </div>
        <UiBadge
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
      <div class="mt-4 flex flex-wrap gap-2">
        <UiButton
          :data-testid="`mobile-edit-access-key-policy-${key.id}`"
          variant="secondary"
          size="sm"
          class="flex-1"
          :disabled="Boolean(key.revoked_at)"
          @click="emit('edit', key)"
        >
          编辑策略
        </UiButton>
        <UiButton
          v-if="!key.revoked_at"
          :data-testid="`mobile-revoke-access-key-${key.id}`"
          variant="ghost"
          size="sm"
          class="flex-1"
          :disabled="busyId === key.id"
          @click="emit('revoke', key)"
        >
          撤销
        </UiButton>
        <UiButton
          :data-testid="`mobile-delete-access-key-${key.id}`"
          variant="danger"
          size="sm"
          class="flex-1"
          :disabled="busyId === key.id"
          @click="emit('delete', key)"
        >
          删除
        </UiButton>
      </div>
    </article>
  </div>
</template>
