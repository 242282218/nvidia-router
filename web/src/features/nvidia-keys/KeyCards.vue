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
    data-testid="key-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="key in keys"
      :key="key.id"
      class="rounded-xl border border-slate-800 bg-slate-900 p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <p class="font-mono text-sm text-slate-200">
            {{ key.masked }}
          </p>
          <p class="mt-1 text-xs text-slate-400">
            {{ statusLabel(key) }}
          </p>
        </div>
        <span class="text-xs text-slate-500">#{{ key.id }}</span>
      </div>
      <p
        v-if="key.cooldown_until"
        class="mt-3 text-xs text-slate-400"
      >
        冷却至 <time :datetime="key.cooldown_until">{{ key.cooldown_until }}</time>
      </p>
      <p class="mt-3 text-xs text-slate-400">
        连续失败 {{ key.consecutive_failures }}
        <span v-if="key.last_error_code"> · {{ key.last_error_code }}</span>
      </p>
      <p
        v-if="key.last_error_at"
        class="mt-1 text-xs text-slate-500"
      >
        最近错误 <time :datetime="key.last_error_at">{{ key.last_error_at }}</time>
      </p>
      <div class="mt-4 grid grid-cols-3 gap-2">
        <button
          data-testid="key-card-toggle"
          class="rounded border border-slate-700 px-2 py-2 text-xs disabled:opacity-40"
          type="button"
          :disabled="busyId === key.id"
          @click="emit('toggle', key)"
        >
          {{ key.enabled ? '停用' : '启用' }}
        </button>
        <button
          data-testid="key-card-test"
          class="rounded border border-slate-700 px-2 py-2 text-xs disabled:opacity-40"
          type="button"
          :disabled="busyId === key.id"
          @click="emit('test', key)"
        >
          单测
        </button>
        <button
          data-testid="key-card-delete"
          class="rounded border border-rose-800 px-2 py-2 text-xs text-rose-300 disabled:opacity-40"
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
      class="rounded-xl border border-dashed border-slate-800 p-6 text-center text-sm text-slate-500"
    >
      暂无 NVIDIA Key。
    </p>
  </div>
</template>
