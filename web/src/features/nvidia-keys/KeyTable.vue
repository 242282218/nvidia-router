<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
import type { DataTableColumn } from '../../shared/ui/dataTable'
import UiDataTable from '../../shared/ui/UiDataTable.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import UiSwitch from '../../shared/ui/UiSwitch.vue'
import { formatDate, keyState } from './state'
import type { NVIDIAKey } from './types'

defineProps<{ keys: NVIDIAKey[]; busyId: number | null }>()

const emit = defineEmits<{
  toggle: [key: NVIDIAKey]
  test: [key: NVIDIAKey]
  remove: [key: NVIDIAKey]
  batchToggle: [enabled: boolean, keys: NVIDIAKey[]]
  batchRemove: [keys: NVIDIAKey[]]
}>()

const columns: DataTableColumn<NVIDIAKey>[] = [
  { key: 'masked', label: 'Key' },
  { key: 'state', label: '状态', sortable: true, value: (row) => (row.enabled && !row.auth_invalid ? 0 : 1) },
  { key: 'failures', label: '失败 / 最近错误', sortable: true, value: (row) => row.consecutive_failures },
  { key: 'actions', label: '操作', align: 'right', width: 'w-44' },
]

function byIds(keys: readonly NVIDIAKey[], ids: readonly (string | number)[]): NVIDIAKey[] {
  const wanted = new Set(ids.map(Number))
  return keys.filter((key) => wanted.has(key.id))
}
</script>

<template>
  <div
    data-testid="key-table"
    class="hidden md:block"
  >
    <UiDataTable
      caption="NVIDIA Key 列表"
      :columns="columns"
      :rows="keys"
      :row-key="(row) => row.id"
      selectable
      max-height="560px"
    >
      <template #cell-masked="{ row }">
        <code class="font-mono-data text-sm text-[var(--color-info)]">{{ row.masked }}</code>
        <span class="mt-0.5 block font-mono-data text-xs text-[var(--color-text-subtle)]">#{{ row.id }}</span>
      </template>
      <template #cell-state="{ row }">
        <UiBadge
          :variant="keyState(row).variant"
          :label="keyState(row).label"
        />
        <p
          v-if="row.cooldown_until"
          class="mt-1.5 text-xs text-[var(--color-text-muted)]"
        >
          冷却至 <span class="font-mono-data">{{ formatDate(row.cooldown_until) }}</span>
          <span class="sr-only">{{ row.cooldown_until }}</span>
        </p>
      </template>
      <template #cell-failures="{ row }">
        <span class="text-[var(--color-text-secondary)]">连续失败 {{ row.consecutive_failures }}</span>
        <span
          v-if="row.last_error_code"
          class="ml-1 font-mono-data text-[var(--color-danger)]"
        >· {{ row.last_error_code }}</span>
        <p
          v-if="row.last_error_at"
          class="mt-1.5 text-xs text-[var(--color-text-muted)]"
        >
          最近错误 <span class="font-mono-data">{{ formatDate(row.last_error_at) }}</span>
          <span class="sr-only">{{ row.last_error_at }}</span>
        </p>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-2">
          <UiSwitch
            :data-testid="`key-table-toggle-${row.id}`"
            :checked="row.enabled"
            :disabled="busyId === row.id"
            :label="row.enabled ? `停用 Key ${row.masked}` : `启用 Key ${row.masked}`"
            @change="emit('toggle', row)"
          />
          <UiButton
            :data-testid="`key-table-test-${row.id}`"
            variant="secondary"
            size="sm"
            :disabled="busyId === row.id"
            @click="emit('test', row)"
          >
            单测
          </UiButton>
          <button
            class="icon-btn-sm hover:bg-[var(--color-danger-background)] hover:text-[var(--color-danger-foreground)]"
            type="button"
            :disabled="busyId === row.id"
            :aria-label="`删除 Key ${row.masked}`"
            @click="emit('remove', row)"
          >
            <UiIcon
              name="trash"
              :size="15"
            />
          </button>
        </div>
      </template>

      <!-- 批量操作条：启停 / 删除（删除走父级确认对话框） -->
      <template #batch="{ selectedKeys, clear }">
        <UiButton
          variant="secondary"
          size="sm"
          :disabled="busyId !== null"
          @click="emit('batchToggle', true, byIds(keys, selectedKeys)); clear()"
        >
          批量启用
        </UiButton>
        <UiButton
          variant="secondary"
          size="sm"
          :disabled="busyId !== null"
          @click="emit('batchToggle', false, byIds(keys, selectedKeys)); clear()"
        >
          批量停用
        </UiButton>
        <UiButton
          variant="danger"
          size="sm"
          :disabled="busyId !== null"
          @click="emit('batchRemove', byIds(keys, selectedKeys))"
        >
          删除…
        </UiButton>
      </template>
    </UiDataTable>
  </div>
</template>
