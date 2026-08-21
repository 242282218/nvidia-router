<script setup lang="ts">
import { computed } from 'vue'

import { formatDate } from '../../shared/format'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import type { ModelHealthBucket, ModelHealthModel, ModelHealthOutcome } from './types'

defineOptions({ name: 'ModelHealthCard' })

const props = defineProps<{ model: ModelHealthModel }>()

type BadgeVariant = 'success' | 'warning' | 'danger' | 'muted' | 'info'

const statusMeta: Record<string, { label: string; variant: BadgeVariant }> = {
  healthy: { label: '正常', variant: 'success' },
  degraded: { label: '降级', variant: 'warning' },
  unavailable: { label: '异常', variant: 'danger' },
  unchecked: { label: '未检测', variant: 'muted' },
  stale: { label: '已过期', variant: 'warning' },
  unconfigured: { label: '未配置', variant: 'muted' },
}

const uncheckedStatus = { label: '未检测', variant: 'muted' as BadgeVariant }
const status = computed(() => statusMeta[props.model.status] ?? uncheckedStatus)
const rateLabel = computed(() => props.model.probe_count > props.model.skipped_count
  ? `${props.model.success_rate.toFixed(1)}%`
  : '—')

function outcomeLabel(outcome: ModelHealthOutcome): string {
  switch (outcome) {
    case 'success': return '全部成功'
    case 'failure': return '全部失败'
    case 'timeout': return '全部超时'
    case 'skipped': return '未配置'
    case 'canceled': return '已取消'
    case 'mixed': return '部分成功'
    default: return '暂无探测'
  }
}

// Warm Restraint 时间格：1.5px 空心描边表达状态色相；仅成功格填充 40% 透明，
// 其余保持纸面底色——整条时间线更轻，密度信息仍由 title 文字承载。
function outlineColor(outcome: ModelHealthOutcome): string {
  switch (outcome) {
    case 'success': return 'var(--color-success)'
    case 'failure': return 'var(--color-danger)'
    case 'timeout':
    case 'mixed': return 'var(--color-warning)'
    case 'skipped': return 'var(--color-info)'
    case 'canceled': return 'var(--color-text-subtle)'
    default: return 'var(--color-border-subtle)'
  }
}

function bucketStyle(outcome: ModelHealthOutcome): Record<string, string> {
  const color = outlineColor(outcome)
  return {
    borderColor: `color-mix(in srgb, ${color} 72%, transparent)`,
    backgroundColor: outcome === 'success' ? `color-mix(in srgb, ${color} 40%, transparent)` : 'transparent',
  }
}

function errorLabel(code?: string): string {
  switch (code) {
    case 'no_credential': return '缺少可用凭据'
    case 'provider_not_configured': return '渠道未配置'
    case 'provider_not_routable': return '渠道暂未接入'
    case 'timeout': return '请求超时'
    case 'network_error': return '网络错误'
    case 'model_not_found': return '模型不存在'
    case 'key_source_unavailable': return 'Key 池暂不可用'
    case 'probe_failed': return '探测失败'
    default: return code ? '探测异常' : '—'
  }
}

function bucketTitle(bucket: ModelHealthBucket): string {
  return `${formatDate(bucket.start, { seconds: true })} · ${outcomeLabel(bucket.outcome)} · ${bucket.probe_count} 次`
}

function timelineLabel(model: ModelHealthModel): string {
  return `${model.display_name || model.public_id} 的 ${model.buckets.length} 个时间段探测状态；当前${status.value.label}`
}

function providerLabel(provider: string): string {
  return provider === 'opencodefree' ? 'OpenCodeFree' : provider.toUpperCase()
}
</script>

<template>
  <article
    class="card min-w-0 p-5 sm:p-6"
    :data-testid="`model-health-card-${model.model_id}`"
  >
    <div class="flex min-w-0 items-start gap-3">
      <div
        class="flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--radius-control)] bg-[var(--color-sunken)] text-[var(--color-text-secondary)]"
        aria-hidden="true"
      >
        <UiIcon
          :name="model.provider === 'opencodefree' ? 'globe' : 'server'"
          :size="17"
        />
      </div>
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="min-w-0 truncate text-sm font-semibold text-[var(--color-text)]">
            {{ model.display_name || model.public_id }}
          </h2>
          <UiBadge
            :variant="status.variant"
            :label="status.label"
          />
          <span
            v-if="!model.enabled"
            class="badge-muted"
          >停用</span>
        </div>
        <p
          class="mt-1 truncate font-mono-data text-xs text-[var(--color-text-muted)]"
          :title="model.public_id"
        >
          {{ model.public_id }}
        </p>
      </div>
      <span class="shrink-0 text-xs text-[var(--color-text-subtle)]">
        {{ providerLabel(model.provider) }}
      </span>
    </div>

    <div class="mt-4 flex items-baseline justify-between gap-3">
      <div>
        <strong class="font-mono-data text-lg font-semibold text-[var(--color-text)]">
          {{ rateLabel }}
        </strong>
        <span class="ml-1 text-xs text-[var(--color-text-muted)]">探测成功率</span>
      </div>
      <span class="text-xs text-[var(--color-text-muted)]">
        {{ model.probe_count }} 次探测
      </span>
    </div>

    <div
      :data-testid="`model-health-timeline-${model.model_id}`"
      class="mt-3 flex h-8 items-stretch gap-1"
      role="img"
      :aria-label="timelineLabel(model)"
    >
      <span
        v-if="model.buckets.length === 0"
        class="min-w-0 flex-1 rounded-[4px] border border-dashed border-[var(--color-border)]"
        aria-hidden="true"
      />
      <span
        v-for="(bucket, index) in model.buckets"
        :key="`${bucket.start}-${index}`"
        :data-testid="`model-health-bucket-${model.model_id}-${index}`"
        class="min-w-0 flex-1 rounded-[4px] border-[1.5px] transition-opacity duration-[var(--duration-micro)] hover:opacity-70"
        :style="bucketStyle(bucket.outcome)"
        :title="bucketTitle(bucket)"
        aria-hidden="true"
      />
    </div>
    <div class="mt-1 flex justify-between text-[11px] text-[var(--color-text-subtle)]">
      <span>{{ model.buckets.length ? formatDate(model.buckets[0]?.start) : '—' }}</span>
      <span>现在</span>
    </div>

    <div class="mt-3 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-[var(--color-text-muted)]">
      <span>最近检测：{{ formatDate(model.last_probe_at, { seconds: true }) }}</span>
      <span v-if="model.last_duration_ms !== undefined">{{ model.last_duration_ms }} ms</span>
    </div>

    <p
      v-if="model.last_error_code"
      class="mt-2 text-xs"
      :class="model.status === 'unconfigured' ? 'text-[var(--color-text-muted)]' : 'text-[var(--color-danger)]'"
    >
      最近异常：{{ errorLabel(model.last_error_code) }}
    </p>

    <details
      :data-testid="`model-health-timeline-details-${model.model_id}`"
      class="mt-3 border-t border-[var(--color-border-subtle)] pt-2 text-xs text-[var(--color-text-muted)]"
    >
      <summary class="cursor-pointer select-none text-[var(--color-text-secondary)]">
        查看时间段详情
      </summary>
      <ol class="mt-2 space-y-1.5">
        <li
          v-for="(bucket, index) in model.buckets"
          :key="`${bucket.end}-${index}`"
          class="flex items-center justify-between gap-3"
        >
          <span>{{ formatDate(bucket.start, { seconds: true }) }}</span>
          <span>{{ outcomeLabel(bucket.outcome) }} · {{ bucket.probe_count }} 次</span>
        </li>
      </ol>
    </details>
  </article>
</template>
