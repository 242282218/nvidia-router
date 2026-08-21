<script setup lang="ts">
import UiBadge from '../../shared/ui/UiBadge.vue'
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
}>()
</script>

<template>
  <div
    data-testid="key-table"
    class="card hidden overflow-hidden md:block"
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
              class="data-table-th w-44 text-right"
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
            <td class="data-table-td">
              <code class="font-mono-data text-sm text-[var(--color-info)]">{{ key.masked }}</code>
              <span class="mt-0.5 block font-mono-data text-xs text-[var(--color-text-subtle)]">#{{ key.id }}</span>
            </td>
            <td class="data-table-td">
              <UiBadge
                :variant="keyState(key).variant"
                :label="keyState(key).label"
              />
              <p
                v-if="key.cooldown_until"
                class="mt-1.5 text-xs text-[var(--color-text-muted)]"
              >
                冷却至 <span class="font-mono-data">{{ formatDate(key.cooldown_until) }}</span>
                <span class="sr-only">{{ key.cooldown_until }}</span>
              </p>
            </td>
            <td class="data-table-td">
              <span class="text-[var(--color-text-secondary)]">连续失败 {{ key.consecutive_failures }}</span>
              <span
                v-if="key.last_error_code"
                class="ml-1 font-mono-data text-[var(--color-danger)]"
              >· {{ key.last_error_code }}</span>
              <p
                v-if="key.last_error_at"
                class="mt-1.5 text-xs text-[var(--color-text-muted)]"
              >
                最近错误 <span class="font-mono-data">{{ formatDate(key.last_error_at) }}</span>
                <span class="sr-only">{{ key.last_error_at }}</span>
              </p>
            </td>
            <td class="data-table-td">
              <div class="flex items-center justify-end gap-2">
                <UiSwitch
                  :data-testid="`key-table-toggle-${key.id}`"
                  :checked="key.enabled"
                  :disabled="busyId === key.id"
                  :label="key.enabled ? `停用 Key ${key.masked}` : `启用 Key ${key.masked}`"
                  @change="emit('toggle', key)"
                />
                <UiButton
                  :data-testid="`key-table-test-${key.id}`"
                  variant="secondary"
                  size="sm"
                  :disabled="busyId === key.id"
                  @click="emit('test', key)"
                >
                  单测
                </UiButton>
                <button
                  class="icon-btn-sm hover:bg-[var(--color-danger-background)] hover:text-[var(--color-danger-foreground)]"
                  type="button"
                  :disabled="busyId === key.id"
                  :aria-label="`删除 Key ${key.masked}`"
                  @click="emit('remove', key)"
                >
                  <UiIcon
                    name="trash"
                    :size="15"
                  />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
