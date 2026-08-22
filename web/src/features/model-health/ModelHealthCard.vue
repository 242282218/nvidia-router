<script setup lang="ts">
import { computed, ref } from 'vue'

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
const detailsOpen = ref(false)
const titleId = computed(() => `model-health-title-${props.model.model_id}`)
const detailsId = computed(() => `model-health-details-${props.model.model_id}`)

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
    class="card card-glow relative min-w-0 overflow-hidden p-4 sm:p-5"
    :data-testid="`model-health-card-${model.model_id}`"
    :data-status="visualStatus"
  >
    <span
      class="absolute inset-x-0 top-0 h-0.5"
      :style="{ backgroundColor: status.color }"
      aria-hidden="true"
    />

    <button
      :id="titleId"
      type="button"
      class="group flex min-h-11 w-full min-w-0 appearance-none items-start gap-3 rounded-[var(--radius-control)] border-0 bg-transparent p-0 text-left focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2"
      :aria-controls="detailsId"
      :aria-expanded="detailsOpen"
      :aria-label="`查看 ${model.display_name || model.public_id} 详情`"
      @click="detailsOpen = !detailsOpen"
    >
      <span
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius-control)] bg-[var(--color-sunken)] text-[var(--color-text-secondary)]"
        aria-hidden="true"
      >
        <UiIcon
          :name="model.provider === 'opencodefree' ? 'globe' : 'server'"
          :size="17"
        />
      </span>
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="flex flex-wrap items-center gap-2">
          <span
            class="min-w-0 truncate text-sm font-semibold text-[var(--color-text)]"
            role="heading"
            aria-level="2"
          >
            {{ model.display_name || model.public_id }}
          </span>
          <UiBadge
            :variant="status.variant"
            :label="status.label"
          />
          <span
            v-if="!model.enabled"
            class="badge-muted"
          >停用</span>
        </span>
        <span
          class="mt-1 truncate font-mono-data text-xs text-[var(--color-text-muted)]"
          :title="model.public_id"
        >
          {{ model.public_id }}
        </span>
      </span>
      <span class="flex shrink-0 items-center gap-2 text-xs text-[var(--color-text-subtle)]">
        <span class="hidden sm:inline">{{ providerLabel(model.provider) }}</span>
        <UiIcon
          name="chevron-down"
          :size="16"
          class="transition-transform duration-[var(--duration-micro)]"
          :class="detailsOpen ? 'rotate-180' : ''"
          aria-hidden="true"
        />
      </span>
    </button>

    <div class="mt-4 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
      <div class="flex items-baseline gap-1.5">
        <strong class="font-mono-data text-lg font-semibold text-[var(--color-text)]">
          {{ rateLabel }}
        </strong>
        <span class="ml-1 text-xs text-[var(--color-text-muted)]">探测成功率</span>
      </div>
      <div class="flex items-baseline gap-1.5">
        <strong class="font-mono-data text-sm font-semibold text-[var(--color-text)]">
          {{ model.probe_count }}
        </strong>
        <span class="text-xs text-[var(--color-text-muted)]">次探测</span>
      </div>
    </div>

    <div class="mt-4">
      <div class="mb-2 flex items-center justify-between gap-3">
        <span class="text-xs font-medium text-[var(--color-text-secondary)]">状态时间线</span>
        <span class="text-[11px] text-[var(--color-text-subtle)]">
          {{ model.buckets.length ? `${model.buckets.length} 个时间段` : '暂无数据' }}
        </span>
      </div>
      <div
        :data-testid="`model-health-timeline-${model.model_id}`"
        class="flex min-h-9 items-stretch gap-1 overflow-hidden"
        role="group"
        :aria-label="timelineLabel(model)"
      >
        <span
          v-if="model.buckets.length === 0"
          class="min-w-0 flex-1 rounded-[4px] border border-dashed border-[var(--color-border)] bg-[var(--color-sunken)]"
          aria-hidden="true"
        />
        <button
          v-for="(bucket, index) in model.buckets"
          :key="`${bucket.start}-${index}`"
          type="button"
          :data-testid="`model-health-bucket-${model.model_id}-${index}`"
          class="min-h-9 min-w-[4px] flex-1 rounded-[4px] border transition-[filter,transform,box-shadow] duration-[var(--duration-micro)] hover:brightness-95 hover:shadow-[var(--shadow-xs)] active:scale-[0.96] focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2"
          :style="bucketStyle(bucket.outcome)"
          :title="bucketTitle(bucket)"
          :aria-label="bucketTitle(bucket)"
          :aria-controls="detailsId"
          :aria-expanded="detailsOpen"
          @click="detailsOpen = true"
        />
      </div>
      <div class="mt-2 grid grid-cols-3 text-[11px] text-[var(--color-text-subtle)]">
        <span>{{ model.buckets.length ? formatDate(model.buckets[0]?.start) : '—' }}</span>
        <span class="text-center">{{ model.buckets.length > 1 ? formatDate(model.buckets[Math.floor(model.buckets.length / 2)]?.start) : '—' }}</span>
        <span class="text-right">现在</span>
      </div>
    </div>

    <div class="mt-3 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-[var(--color-text-muted)]">
      <span>最近检测：{{ formatDate(model.last_probe_at, { seconds: true }) }}</span>
      <span v-if="model.last_duration_ms !== undefined">{{ model.last_duration_ms }} ms</span>
    </div>

    <p
      v-if="model.last_error_code"
      class="mt-2 text-xs"
      :class="errorToneClass"
    >
      最近异常：{{ errorLabel(model.last_error_code) }}
    </p>

    <section
      v-if="detailsOpen"
      :id="detailsId"
      :data-testid="`model-health-timeline-details-${model.model_id}`"
      class="mt-4 border-t border-[var(--color-border-subtle)] pt-3 text-xs text-[var(--color-text-muted)]"
      :aria-labelledby="titleId"
    >
      <div class="flex items-center justify-between gap-3">
        <h3 class="font-medium text-[var(--color-text-secondary)]">
          时间段详情
        </h3>
        <button
          type="button"
          class="min-h-9 rounded-[var(--radius-control)] px-2 text-[var(--color-text-subtle)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2"
          @click="detailsOpen = false"
        >
          收起
        </button>
      </div>
      <ol class="mt-2 space-y-1.5">
        <li
          v-for="(bucket, index) in model.buckets"
          :key="`${bucket.end}-${index}`"
          class="flex items-center justify-between gap-3"
        >
          <span class="flex min-w-0 items-center gap-2">
            <span
              class="h-2 w-2 shrink-0 rounded-[2px]"
              :style="bucketStyle(bucket.outcome)"
              aria-hidden="true"
            />
            <span class="truncate">{{ formatDate(bucket.start, { seconds: true }) }}</span>
          </span>
          <span class="shrink-0">{{ outcomeLabel(bucket.outcome) }} · {{ bucket.probe_count }} 次</span>
        </li>
      </ol>
      <p
        v-if="model.buckets.length === 0"
        class="mt-2 rounded-[var(--radius-control)] bg-[var(--color-sunken)] px-3 py-2"
      >
        当前时间范围没有可用探测数据。
      </p>
    </section>
  </article>
</template>
