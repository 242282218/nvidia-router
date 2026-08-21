<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import type { DataTableColumn } from '../../shared/ui/dataTable'
import UiDataTable from '../../shared/ui/UiDataTable.vue'
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

const columns: DataTableColumn<AccessKey>[] = [
  { key: 'name', label: '名称', sortable: true, value: (row) => row.name },
  { key: 'prefix', label: '前缀' },
  { key: 'created_at', label: '创建时间', sortable: true, value: (row) => row.created_at },
  { key: 'last_used_at', label: '最后使用', sortable: true, value: (row) => row.last_used_at ?? '' },
  { key: 'budget', label: 'Token 预算', sortable: true, value: (row) => budgetUsagePercent(row) },
  { key: 'state', label: '状态' },
  { key: 'actions', label: '操作', align: 'right' },
]
</script>

<template>
  <div
    data-testid="access-key-table"
    class="hidden md:block"
  >
    <UiDataTable
      :caption="`Access Key 列表，共 ${keys.length} 条`"
      :columns="columns"
      :rows="keys"
      :row-key="(row) => row.id"
      max-height="560px"
    >
      <template #cell-name="{ row }">
        <span class="font-medium text-[var(--color-text)]">{{ row.name }}</span>
      </template>
      <template #cell-prefix="{ row }">
        <span class="font-mono-data text-[var(--color-info)]">{{ row.key_prefix }}</span>
      </template>
      <template #cell-created_at="{ row }">
        {{ formatKeyValue(row.created_at) }}
      </template>
      <template #cell-last_used_at="{ row }">
        {{ formatKeyValue(row.last_used_at) }}
      </template>
      <template #cell-budget="{ row }">
        <div
          v-if="row.token_budget > 0"
          class="w-32"
          :data-testid="`access-key-budget-${row.id}`"
        >
          <div class="flex justify-between font-mono-data text-xs text-[var(--color-text-muted)]">
            <span>{{ formatTokens(row.consumed_tokens) }} / {{ formatTokens(row.token_budget) }}</span>
            <span>{{ budgetUsagePercent(row) }}%</span>
          </div>
          <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-border)]">
            <div
              class="h-full rounded-full transition-[width] duration-300"
              :class="budgetUsagePercent(row) >= 90 ? 'bg-[var(--color-danger)]' : budgetUsagePercent(row) >= 60 ? 'bg-[var(--color-warning)]' : 'bg-[var(--color-success)]'"
              :style="{ width: `${budgetUsagePercent(row)}%` }"
            />
          </div>
        </div>
        <span
          v-else
          class="text-xs text-[var(--color-text-subtle)]"
        >
          不限
        </span>
      </template>
      <template #cell-state="{ row }">
        <UiBadge
          :variant="keyState(row).variant"
          :label="keyState(row).label"
        />
        <span
          v-if="row.expires_at && keyState(row).label !== '已过期'"
          class="mt-1.5 block text-xs text-[var(--color-text-subtle)]"
        >
          {{ formatKeyValue(row.expires_at) }} 过期
        </span>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-1.5">
          <UiButton
            :data-testid="`edit-access-key-policy-${row.id}`"
            variant="secondary"
            size="sm"
            :disabled="Boolean(row.revoked_at)"
            @click="emit('edit', row)"
          >
            编辑策略
          </UiButton>
          <UiButton
            v-if="!row.revoked_at"
            :data-testid="`revoke-access-key-${row.id}`"
            variant="ghost"
            size="sm"
            :disabled="busyId === row.id"
            @click="emit('revoke', row)"
          >
            撤销
          </UiButton>
          <UiButton
            :data-testid="`delete-access-key-${row.id}`"
            variant="danger"
            size="sm"
            :disabled="busyId === row.id"
            @click="emit('delete', row)"
          >
            删除
          </UiButton>
        </div>
      </template>
    </UiDataTable>
  </div>
</template>
