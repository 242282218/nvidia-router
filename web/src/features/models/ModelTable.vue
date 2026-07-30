<script setup lang="ts">
import type { Model } from './types'

defineProps<{ models: Model[]; busyId: number | null }>()
const emit = defineEmits<{
  toggle: [model: Model]
  unblock: [keyId: number, model: Model]
}>()

function audioNeedsVerification(model: Model): boolean {
  return (model.kind === 'asr' || model.kind === 'tts') && !model.capability_verified_at
}

function enablingIsBlocked(model: Model): boolean {
  return !model.enabled && audioNeedsVerification(model)
}
</script>

<template>
  <div
    data-testid="model-table"
    class="hidden overflow-x-auto rounded-xl border border-slate-800 bg-slate-900 md:block"
  >
    <table class="min-w-full text-left text-sm">
      <thead class="border-b border-slate-800 text-slate-400">
        <tr>
          <th class="px-4 py-3">
            模型
          </th>
          <th class="px-4 py-3">
            Kind
          </th>
          <th class="px-4 py-3">
            能力
          </th>
          <th class="px-4 py-3">
            状态
          </th>
          <th class="px-4 py-3 text-right">
            操作
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="model in models"
          :key="model.id"
          class="border-b border-slate-800/80 last:border-0"
        >
          <td class="px-4 py-3">
            <p class="font-medium">
              {{ model.display_name }}
            </p>
            <p class="mt-1 font-mono text-xs text-slate-500">
              {{ model.public_id }}
            </p>
          </td>
          <td class="px-4 py-3 font-mono text-indigo-300">
            {{ model.kind }}
          </td>
          <td class="px-4 py-3 text-xs text-slate-300">
            <span :class="model.supports_vision ? 'text-emerald-300' : 'text-slate-500'">
              Vision {{ model.supports_vision ? '✓' : '—' }}
            </span>
            <span
              class="ml-2"
              :class="model.supports_tools ? 'text-emerald-300' : 'text-slate-500'"
            >
              Tools {{ model.supports_tools ? '✓' : '—' }}
            </span>
            <span
              class="ml-2"
              :class="model.supports_reasoning ? 'text-emerald-300' : 'text-slate-500'"
            >
              Reasoning {{ model.supports_reasoning ? '✓' : '—' }}
            </span>
          </td>
          <td class="px-4 py-3">
            <span :class="model.enabled ? 'text-emerald-300' : 'text-slate-400'">
              {{ model.enabled ? '启用' : '停用' }}
            </span>
            <p
              v-if="model.capability_verified_at"
              class="mt-1 text-xs text-slate-500"
            >
              能力已验证 <time :datetime="model.capability_verified_at">{{ model.capability_verified_at }}</time>
            </p>
            <p
              v-else-if="audioNeedsVerification(model)"
              class="mt-1 max-w-52 text-xs text-amber-300"
            >
              需要先完成真实音频能力测试
            </p>
            <div
              v-if="model.blocked_by_key_ids?.length"
              class="mt-2 space-y-1 text-xs text-amber-300"
            >
              <p>已 block：</p>
              <button
                v-for="keyId in model.blocked_by_key_ids"
                :key="keyId"
                :data-testid="`model-table-unblock-${keyId}`"
                class="block underline hover:text-amber-100 disabled:opacity-40"
                type="button"
                :disabled="busyId === model.id"
                @click="emit('unblock', keyId, model)"
              >
                Key #{{ keyId }} · 手测恢复
              </button>
            </div>
          </td>
          <td class="space-x-2 px-4 py-3 text-right">
            <button
              data-testid="model-enable"
              class="rounded border border-slate-700 px-2 py-1 text-xs disabled:cursor-not-allowed disabled:opacity-40"
              type="button"
              :disabled="enablingIsBlocked(model) || busyId === model.id"
              @click="emit('toggle', model)"
            >
              {{ model.enabled ? '停用' : '启用' }}
            </button>
          </td>
        </tr>
        <tr v-if="models.length === 0">
          <td
            colspan="5"
            class="px-4 py-8 text-center text-slate-500"
          >
            暂无模型白名单。
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
