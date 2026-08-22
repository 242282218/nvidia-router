<script setup lang="ts">
import { computed } from 'vue'

import { formatDate } from '../../shared/format'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import { displayStatus } from './status'
import type { ModelHealthBucket, ModelHealthModel, ModelHealthOutcome } from './types'

defineOptions({ name: 'ModelHealthCard' })

const props = defineProps<{ model: ModelHealthModel }>()

type BadgeVariant = 'success' | 'warning' | 'danger' | 'muted' | 'info'
type StatusMeta = { label: string; variant: BadgeVariant; color: string }

const statusMeta: Record<string, StatusMeta> = {
  healthy: { label: '健康', variant: 'success', color: 'var(--color-success)' },
  degraded: { label: '降级', variant: 'warning', color: 'var(--color-warning)' },
  unavailable: { label: '异常', variant: 'danger', color: 'var(--color-danger)' },
  unchecked: { label: '无数据', variant: 'muted', color: 'var(--color-text-subtle)' },
  stale: { label: '无数据', variant: 'muted', color: 'var(--color-text-subtle)' },
  unconfigured: { label: '无数据', variant: 'muted', color: 'var(--color-text-subtle)' },
}

const uncheckedStatus: StatusMeta = {
  label: '无数据',
  variant: 'muted',
  color: 'var(--color-text-subtle)',
}
const visualStatus = computed(() => displayStatus(props.model))
const status = computed(() => statusMeta[visualStatus.value] ?? uncheckedStatus)
const errorToneClass = computed(() => visualStatus.value === 'unchecked'
  ? 'text-[var(--color-text-muted)]'
  : visualStatus.value === 'unavailable'
    ? 'text-[var(--color-danger)]'
    : 'text-[var(--color-warning)]')
const rateLabel = computed(() => props.model.probe_count > props.model.skipped_count
  ? `${props.model.success_rate.toFixed(1)}%`
  : '—')
const titleId = computed(() => `model-health-title-${props.model.model_id}`)

function outcomeLabel(outcome: ModelHealthOutcome): string {
  switch (outcome) {
    case 'success': return '全部成功'
    case 'failure': return '全部失败'
    case 'timeout': return '全部超时'
    case 'skipped': return '无数据'
    case 'canceled': return '已取消'
    case 'mixed': return '部分成功'
    case 'empty': return '无数据'
    default: return '暂无探测'
  }
}

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
  const color = outcome === 'skipped' || outcome === 'canceled'
    ? 'var(--color-text-subtle)'
    : outlineColor(outcome)
  return {
    borderColor: `color-mix(in srgb, ${color} 78%, transparent)`,
    backgroundColor: `color-mix(in srgb, ${color} 86%, var(--color-surface))`,
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
    class="card card-glow relative min-w-0 overflow-hidden p-4 sm:p-[18px]"
    :data-testid="`model-health-card-${model.model_id}`"
    :data-status="visualStatus"
    :aria-labelledby="titleId"
  >
    <span
      class="absolute inset-x-0 top-0 h-0.5"
      :style="{ backgroundColor: status.color }"
      aria-hidden="true"
    />

    <div class="flex min-w-0 items-start gap-3">
      <span
        class="flex h-10 w-10 shrink-0 items-center justify-center rounded-[10px] bg-[var(--color-sunken)] text-[var(--color-text-secondary)] ring-1 ring-inset ring-[var(--color-border-subtle)]"
        aria-hidden="true"
      >
        <UiIcon
          :name="model.provider === 'opencodefree' ? 'globe' : 'server'"
          :size="17"
        />
      </span>
      <div class="flex min-w-0 flex-1 flex-col pt-0.5">
        <span class="flex flex-wrap items-center gap-2">
          <h2
            :id="titleId"
            class="m-0 min-w-0 flex-1 truncate text-[15px] font-semibold leading-5 text-[var(--color-text)]"
            :title="model.display_name || model.public_id"
          >
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
        </span>
        <p
          class="m-0 mt-1 flex min-w-0 items-center gap-1.5 truncate text-xs text-[var(--color-text-muted)]"
          :title="model.public_id"
        >
          <span class="shrink-0 font-medium text-[var(--color-text-secondary)]">{{ providerLabel(model.provider) }}</span>
          <span class="text-[var(--color-text-subtle)]">·</span>
          <span class="min-w-0 truncate font-mono-data">{{ model.public_id }}</span>
        </p>
      </div>
      <div
        class="shrink-0 rounded-[var(--radius-control)] px-3 py-2 text-right"
        :style="{ backgroundColor: `color-mix(in srgb, ${status.color} 10%, var(--color-surface))` }"
      >
        <strong
          class="block font-mono-data text-[21px] font-semibold leading-none tracking-[var(--tracking-display)]"
          :style="{ color: status.color }"
          :data-testid="`model-health-rate-${model.model_id}`"
        >
          {{ rateLabel }}
        </strong>
        <span class="mt-1 block text-[11px] text-[var(--color-text-muted)]">成功率</span>
      </div>
    </div>

    <dl class="m-0 mt-3 grid grid-cols-3 gap-px overflow-hidden rounded-[var(--radius-control)] bg-[var(--color-border-subtle)]">
      <div class="min-w-0 bg-[var(--color-sunken)] px-2.5 py-2">
        <dt class="m-0 type-label truncate">
          探测次数
        </dt>
        <dd class="m-0 mt-1 font-mono-data text-sm font-semibold text-[var(--color-text)]">
          {{ model.probe_count }}
        </dd>
      </div>
      <div class="min-w-0 bg-[var(--color-sunken)] px-2.5 py-2">
        <dt class="m-0 type-label truncate">
          连续失败
        </dt>
        <dd
          class="m-0 mt-1 font-mono-data text-sm font-semibold"
          :class="model.consecutive_failures > 0 ? 'text-[var(--color-danger)]' : 'text-[var(--color-text)]'"
        >
          {{ model.consecutive_failures }}
        </dd>
      </div>
      <div class="min-w-0 bg-[var(--color-sunken)] px-2.5 py-2">
        <dt class="m-0 type-label truncate">
          最近延迟
        </dt>
        <dd class="m-0 mt-1 truncate font-mono-data text-sm font-semibold text-[var(--color-text)]">
          <template v-if="model.last_duration_ms !== undefined">
            {{ model.last_duration_ms }}<span class="ml-0.5 text-[11px] font-normal text-[var(--color-text-muted)]">ms</span>
          </template>
          <template v-else>
            —
          </template>
        </dd>
      </div>
    </dl>

    <div class="mt-3">
      <div class="mb-2 flex items-center justify-between gap-3">
        <span class="type-label text-[var(--color-text-secondary)]">状态时间线</span>
        <span class="text-[11px] text-[var(--color-text-subtle)]">
          {{ model.buckets.length ? `${model.buckets.length} 个时间段` : '暂无数据' }}
        </span>
      </div>
      <div
        :data-testid="`model-health-timeline-${model.model_id}`"
        class="flex min-h-9 items-stretch gap-px overflow-hidden rounded-[var(--radius-control)] bg-[var(--color-sunken)] p-1"
        role="group"
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
          class="min-h-7 min-w-0 flex-1 rounded-[3px] border"
          :style="bucketStyle(bucket.outcome)"
          :title="bucketTitle(bucket)"
          :aria-label="bucketTitle(bucket)"
          role="img"
        />
      </div>
      <div class="mt-2 grid grid-cols-3 text-[11px] text-[var(--color-text-subtle)]">
        <span>{{ model.buckets.length ? formatDate(model.buckets[0]?.start) : '—' }}</span>
        <span class="text-center">{{ model.buckets.length > 1 ? formatDate(model.buckets[Math.floor(model.buckets.length / 2)]?.start) : '—' }}</span>
        <span class="text-right">现在</span>
      </div>
    </div>

    <div class="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 border-t border-[var(--color-border-subtle)] pt-2.5 text-xs text-[var(--color-text-muted)]">
      <span>最近检测</span>
      <time
        class="font-mono-data text-[var(--color-text-secondary)]"
        :datetime="model.last_probe_at"
      >
        {{ formatDate(model.last_probe_at, { seconds: true }) }}
      </time>
    </div>

    <p
      v-if="model.last_error_code"
      class="m-0 mt-1 flex items-center gap-1.5 text-xs"
      :class="errorToneClass"
    >
      <UiIcon
        name="warning"
        :size="14"
        aria-hidden="true"
      />
      <span>最近异常：{{ errorLabel(model.last_error_code) }}</span>
    </p>
  </article>
</template>
