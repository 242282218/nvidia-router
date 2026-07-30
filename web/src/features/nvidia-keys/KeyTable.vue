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
</script>

<template>
  <div
    data-testid="key-table"
    class="hidden overflow-x-auto rounded-xl border border-slate-800 bg-slate-900 md:block"
  >
    <table class="min-w-full text-left text-sm">
      <thead class="border-b border-slate-800 text-slate-400">
        <tr>
          <th class="px-4 py-3">
            Key
          </th>
          <th class="px-4 py-3">
            状态
          </th>
          <th class="px-4 py-3">
            失败/最近错误
          </th>
          <th class="px-4 py-3 text-right">
            操作
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="key in keys"
          :key="key.id"
          class="border-b border-slate-800/80 last:border-0"
        >
          <td class="px-4 py-3 font-mono text-slate-200">
            {{ key.masked }}
          </td>
          <td class="px-4 py-3">
            <span :class="key.enabled && !key.auth_invalid ? 'text-emerald-300' : 'text-amber-300'">
              {{ statusLabel(key) }}
            </span>
            <p
              v-if="key.cooldown_until"
              class="mt-1 text-xs text-slate-500"
            >
              冷却至 <time :datetime="key.cooldown_until">{{ key.cooldown_until }}</time>
            </p>
          </td>
          <td class="px-4 py-3 text-slate-400">
            <span>连续失败 {{ key.consecutive_failures }}</span>
            <span v-if="key.last_error_code"> · {{ key.last_error_code }}</span>
            <p
              v-if="key.last_error_at"
              class="mt-1 text-xs text-slate-500"
            >
              最近错误 <time :datetime="key.last_error_at">{{ key.last_error_at }}</time>
            </p>
          </td>
          <td class="space-x-2 px-4 py-3 text-right">
            <button
              class="rounded border border-slate-700 px-2 py-1 text-xs hover:border-slate-500 disabled:opacity-40"
              type="button"
              :disabled="busyId === key.id"
              @click="emit('toggle', key)"
            >
              {{ key.enabled ? '停用' : '启用' }}
            </button>
            <button
              class="rounded border border-slate-700 px-2 py-1 text-xs hover:border-slate-500 disabled:opacity-40"
              type="button"
              :disabled="busyId === key.id"
              @click="emit('test', key)"
            >
              单测
            </button>
            <button
              class="rounded border border-rose-800 px-2 py-1 text-xs text-rose-300 hover:border-rose-600 disabled:opacity-40"
              type="button"
              :disabled="busyId === key.id"
              @click="emit('remove', key)"
            >
              删除
            </button>
          </td>
        </tr>
        <tr v-if="keys.length === 0">
          <td
            class="px-4 py-8 text-center text-slate-500"
            colspan="4"
          >
            暂无 NVIDIA Key。
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
