<script setup lang="ts">
import StatusBadge from '../../shared/components/StatusBadge.vue'
import { formatDate, keyState } from './state'
import type { NVIDIAKey } from './types'

defineProps<{ keys: NVIDIAKey[]; busyId: number | null; confirmingId: number | null }>()

const emit = defineEmits<{
  toggle: [key: NVIDIAKey]
  test: [key: NVIDIAKey]
  remove: [key: NVIDIAKey]
}>()
</script>

<template>
  <div
    data-testid="key-table"
    class="hidden overflow-hidden rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] md:block"
  >
    <div
      class="overflow-x-auto focus-within:ring-2 focus-within:ring-[color-mix(in_srgb,var(--color-focus)_40%,transparent)]"
      tabindex="0"
      aria-label="NVIDIA Key 表，可横向滚动"
    >
      <table class="data-table">
        <caption class="sr-only">
          NVIDIA Key 列表，共 {{ keys.length }} 条
        </caption>
        <thead>
          <tr>
            <th
              class="data-table-th"
              scope="col"
            >
              Key
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              状态
            </th>
            <th
              class="data-table-th"
              scope="col"
            >
              失败 / 最近错误
            </th>
            <th
              class="data-table-th text-right"
              scope="col"
            >
              操作
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-[var(--color-border)]">
          <tr
            v-for="key in keys"
            :key="key.id"
            class="transition-colors hover:bg-[var(--color-hover)]"
          >
            <td class="data-table-td">
              <code class="font-mono text-sm text-[var(--color-info)]">{{ key.masked }}</code>
              <span class="mt-1 block font-mono text-xs text-[var(--color-text-subtle)]">#{{ key.id }}</span>
            </td>
            <td class="data-table-td">
              <StatusBadge
                :variant="keyState(key).variant"
                :label="keyState(key).label"
              />
              <p
                v-if="key.cooldown_until"
                class="mt-2 text-xs text-[var(--color-text-muted)]"
              >
                冷却至 <span class="font-mono">{{ formatDate(key.cooldown_until) }}</span>
                <span class="sr-only">{{ key.cooldown_until }}</span>
              </p>
            </td>
            <td class="data-table-td">
              <span class="text-[var(--color-text-secondary)]">连续失败 {{ key.consecutive_failures }}</span>
              <span
                v-if="key.last_error_code"
                class="ml-1 font-mono text-[var(--color-danger)]"
              >· {{ key.last_error_code }}</span>
              <p
                v-if="key.last_error_at"
                class="mt-2 text-xs text-[var(--color-text-muted)]"
              >
                最近错误 <span class="font-mono">{{ formatDate(key.last_error_at) }}</span>
                <span class="sr-only">{{ key.last_error_at }}</span>
              </p>
            </td>
            <td class="data-table-td text-right">
              <div class="flex justify-end gap-1.5">
                <button
                  :data-testid="`key-table-toggle-${key.id}`"
                  class="btn-secondary"
                  type="button"
                  :disabled="busyId === key.id"
                  @click="emit('toggle', key)"
                >
                  {{ key.enabled ? '停用' : '启用' }}
                </button>
                <button
                  :data-testid="`key-table-test-${key.id}`"
                  class="btn-secondary"
                  type="button"
                  :disabled="busyId === key.id"
                  @click="emit('test', key)"
                >
                  单测
                </button>
                <button
                  class="btn-danger"
                  type="button"
                  :disabled="busyId === key.id"
                  @click="emit('remove', key)"
                >
                  {{ confirmingId === key.id ? '确认删除？' : '删除' }}
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="keys.length === 0">
            <td
              class="px-4 py-8 text-center text-[var(--color-text-muted)]"
              colspan="4"
            >
              暂无 NVIDIA Key。
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
