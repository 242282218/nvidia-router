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
    data-testid="access-key-table"
    class="hidden overflow-x-auto md:block"
  >
    <table class="data-table min-w-full">
      <caption class="sr-only">
        Access Key 列表，共 {{ keys.length }} 条
      </caption>
      <thead>
        <tr>
          <th
            class="data-table-th"
            scope="col"
          >
            名称
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            前缀
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            创建时间
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            最后使用
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            Token 预算
          </th>
          <th
            class="data-table-th"
            scope="col"
          >
            状态
          </th>
          <th
            class="data-table-th text-right"
            scope="col"
          >
            操作
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="key in keys"
          :key="key.id"
          class="data-table-row"
        >
          <td class="data-table-td font-medium text-[var(--color-text)]">
            {{ key.name }}
          </td>
          <td class="data-table-td font-mono-data text-[var(--color-info)]">
            {{ key.key_prefix }}
          </td>
          <td class="data-table-td text-[var(--color-text-secondary)]">
            {{ formatKeyValue(key.created_at) }}
          </td>
          <td class="data-table-td text-[var(--color-text-secondary)]">
            {{ formatKeyValue(key.last_used_at) }}
          </td>
          <td class="data-table-td">
            <div
              v-if="key.token_budget > 0"
              class="w-32"
              :data-testid="`access-key-budget-${key.id}`"
            >
              <div class="flex justify-between font-mono-data text-xs text-[var(--color-text-muted)]">
                <span>{{ formatTokens(key.consumed_tokens) }} / {{ formatTokens(key.token_budget) }}</span>
                <span>{{ budgetUsagePercent(key) }}%</span>
              </div>
              <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-border)]">
                <div
                  class="h-full rounded-full transition-[width] duration-300"
                  :class="budgetUsagePercent(key) >= 90 ? 'bg-[var(--color-danger)]' : budgetUsagePercent(key) >= 60 ? 'bg-[var(--color-warning)]' : 'bg-[var(--color-success)]'"
                  :style="{ width: `${budgetUsagePercent(key)}%` }"
                />
              </div>
            </div>
            <span
              v-else
              class="text-xs text-[var(--color-text-subtle)]"
            >
              不限
            </span>
          </td>
          <td class="data-table-td">
            <UiBadge
              :variant="keyState(key).variant"
              :label="keyState(key).label"
            />
            <span
              v-if="key.expires_at && keyState(key).label !== '已过期'"
              class="mt-1.5 block text-xs text-[var(--color-text-subtle)]"
            >
              {{ formatKeyValue(key.expires_at) }} 过期
            </span>
          </td>
          <td class="data-table-td">
            <div class="flex justify-end gap-1.5">
              <UiButton
                :data-testid="`edit-access-key-policy-${key.id}`"
                variant="secondary"
                size="sm"
                :disabled="Boolean(key.revoked_at)"
                @click="emit('edit', key)"
              >
                编辑策略
              </UiButton>
              <UiButton
                v-if="!key.revoked_at"
                :data-testid="`revoke-access-key-${key.id}`"
                variant="ghost"
                size="sm"
                :disabled="busyId === key.id"
                @click="emit('revoke', key)"
              >
                撤销
              </UiButton>
              <UiButton
                :data-testid="`delete-access-key-${key.id}`"
                variant="danger"
                size="sm"
                :disabled="busyId === key.id"
                @click="emit('delete', key)"
              >
                删除
              </UiButton>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
