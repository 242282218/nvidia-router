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

function capBadge(supported: boolean): string {
  return supported ? 'badge-success' : 'badge-muted'
}
</script>

<template>
  <div
    data-testid="model-table"
    class="hidden overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] md:block"
  >
    <table class="data-table">
      <thead>
        <tr>
          <th class="data-table-th">
            模型
          </th>
          <th class="data-table-th">
            Kind
          </th>
          <th class="data-table-th">
            能力
          </th>
          <th class="data-table-th">
            状态
          </th>
          <th class="data-table-th text-right">
            操作
          </th>
        </tr>
      </thead>
      <tbody class="divide-y divide-[var(--color-border)]">
        <tr
          v-for="model in models"
          :key="model.id"
          class="transition-colors hover:bg-[var(--color-hover)]"
        >
          <td class="data-table-td">
            <p class="font-medium text-[var(--color-text)]">
              {{ model.display_name }}
            </p>
            <p class="mt-0.5 font-mono text-xs text-[var(--color-text-muted)]">
              {{ model.public_id }}
            </p>
          </td>
          <td class="data-table-td">
            <span class="badge-info">{{ model.kind }}</span>
          </td>
          <td class="data-table-td">
            <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">
              <span :class="capBadge(model.supports_vision)">Vision {{ model.supports_vision ? '✓' : '—' }}</span>
              <span :class="capBadge(model.supports_tools)">Tools {{ model.supports_tools ? '✓' : '—' }}</span>
              <span :class="capBadge(model.supports_reasoning)">Reasoning {{ model.supports_reasoning ? '✓' : '—' }}</span>
            </div>
          </td>
          <td class="data-table-td">
            <span
              v-if="model.enabled"
              class="badge-success"
            >启用</span>
            <span
              v-else
              class="badge-muted"
            >停用</span>
            <p
              v-if="model.capability_verified_at"
              class="mt-1 text-xs text-[var(--color-text-muted)]"
            >
              已验证
            </p>
            <p
              v-else-if="audioNeedsVerification(model)"
              class="mt-1 text-xs text-[var(--color-warning)]"
            >
              需要先完成真实音频能力测试
            </p>
            <div
              v-if="model.blocked_by_key_ids?.length"
              class="mt-2 space-y-1"
            >
              <p class="text-xs text-[var(--color-warning)]">
                已 block：
              </p>
              <button
                v-for="keyId in model.blocked_by_key_ids"
                :key="keyId"
                :data-testid="`model-table-unblock-${keyId}`"
                class="block text-xs text-[#F87171] underline hover:text-[#FCA5A5] disabled:opacity-40"
                type="button"
                :disabled="busyId === model.id"
                @click="emit('unblock', keyId, model)"
              >
                Key #{{ keyId }} · 手测恢复
              </button>
            </div>
          </td>
          <td class="data-table-td text-right">
            <button
              data-testid="model-enable"
              class="btn-secondary rounded-md px-3 py-1 text-xs"
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
            class="px-4 py-8 text-center text-[var(--color-text-muted)]"
          >
            暂无模型白名单。
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>