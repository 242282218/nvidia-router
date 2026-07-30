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
    data-testid="model-cards"
    class="space-y-3 md:hidden"
  >
    <article
      v-for="model in models"
      :key="model.id"
      class="rounded-xl border border-slate-800 bg-slate-900 p-4"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <h3 class="font-medium">
            {{ model.display_name }}
          </h3>
          <p class="mt-1 font-mono text-xs text-slate-500">
            {{ model.public_id }}
          </p>
        </div>
        <span class="font-mono text-xs text-indigo-300">{{ model.kind }}</span>
      </div>
      <div class="mt-3 flex flex-wrap gap-2 text-xs">
        <span :class="model.supports_vision ? 'text-emerald-300' : 'text-slate-500'">
          Vision {{ model.supports_vision ? '✓' : '—' }}
        </span>
        <span :class="model.supports_tools ? 'text-emerald-300' : 'text-slate-500'">
          Tools {{ model.supports_tools ? '✓' : '—' }}
        </span>
        <span :class="model.supports_reasoning ? 'text-emerald-300' : 'text-slate-500'">
          Reasoning {{ model.supports_reasoning ? '✓' : '—' }}
        </span>
      </div>
      <p
        class="mt-3 text-sm"
        :class="model.enabled ? 'text-emerald-300' : 'text-slate-400'"
      >
        {{ model.enabled ? '启用' : '停用' }}
      </p>
      <p
        v-if="model.capability_verified_at"
        class="mt-1 text-xs text-slate-500"
      >
        能力已验证 <time :datetime="model.capability_verified_at">{{ model.capability_verified_at }}</time>
      </p>
      <p
        v-else-if="audioNeedsVerification(model)"
        class="mt-1 text-xs text-amber-300"
      >
        需要先完成真实音频能力测试
      </p>
      <div
        v-if="model.blocked_by_key_ids?.length"
        class="mt-3 space-y-1 text-xs text-amber-300"
      >
        <p>已 block：</p>
        <button
          v-for="keyId in model.blocked_by_key_ids"
          :key="keyId"
          :data-testid="`model-unblock-${keyId}`"
          class="block underline disabled:opacity-40"
          type="button"
          :disabled="busyId === model.id"
          @click="emit('unblock', keyId, model)"
        >
          Key #{{ keyId }} · 手测恢复
        </button>
      </div>
      <button
        data-testid="model-card-toggle"
        class="mt-4 w-full rounded border border-slate-700 px-3 py-2 text-sm disabled:cursor-not-allowed disabled:opacity-40"
        type="button"
        :disabled="enablingIsBlocked(model) || busyId === model.id"
        @click="emit('toggle', model)"
      >
        {{ model.enabled ? '停用' : '启用' }}
      </button>
    </article>
    <p
      v-if="models.length === 0"
      class="rounded-xl border border-dashed border-slate-800 p-6 text-center text-sm text-slate-500"
    >
      暂无模型白名单。
    </p>
  </div>
</template>
