<script setup lang="ts">
import { computed, ref } from 'vue'

import { formatDate } from '../../shared/format'
import UiIcon from '../../shared/ui/UiIcon.vue'
import { horizontalPlacement, type HorizontalPlacement } from '../../shared/ui/overlayPosition'
import { displayStatus } from './status'
import type { ModelHealthBucket, ModelHealthModel, ModelHealthOutcome } from './types'

defineOptions({ name: 'ModelHealthCard' })

const props = defineProps<{ model: ModelHealthModel }>()

type BadgeVariant = 'success' | 'warning' | 'danger' | 'muted'
type StatusMeta = { label: string; variant: BadgeVariant; color: string; dotColor: string; borderColor: string }

// 状态色一律走主题 token：写死的 Tailwind 色阶在暗色主题下不会跟着切换，
// 而本项目的暗色是 [data-theme='dark'] 而不是 .dark，`dark:` 变体根本不生效。
// 卡片描边用状态色与边框 token 混色，浅色/暗色都得到同一份语义强度。
const statusBorder = {
  healthy: 'border-[color-mix(in_srgb,var(--color-success)_30%,var(--color-border))] hover:border-[color-mix(in_srgb,var(--color-success)_55%,var(--color-border))]',
  degraded: 'border-[color-mix(in_srgb,var(--color-warning)_30%,var(--color-border))] hover:border-[color-mix(in_srgb,var(--color-warning)_55%,var(--color-border))]',
  unavailable: 'border-[color-mix(in_srgb,var(--color-danger)_30%,var(--color-border))] hover:border-[color-mix(in_srgb,var(--color-danger)_55%,var(--color-border))]',
} as const

const mutedStatus: StatusMeta = {
  label: '无数据',
  variant: 'muted',
  color: 'var(--color-text-subtle)',
  dotColor: 'var(--color-text-subtle)',
  borderColor: 'border-[var(--color-border)] hover:border-[var(--color-border-strong)]',
}

const statusMeta: Record<string, StatusMeta> = {
  healthy: {
    label: '健康',
    variant: 'success',
    color: 'var(--color-success)',
    dotColor: 'var(--color-success)',
    borderColor: statusBorder.healthy,
  },
  degraded: {
    label: '降级',
    variant: 'warning',
    color: 'var(--color-warning)',
    dotColor: 'var(--color-warning)',
    borderColor: statusBorder.degraded,
  },
  unavailable: {
    label: '异常',
    variant: 'danger',
    color: 'var(--color-danger)',
    dotColor: 'var(--color-danger)',
    borderColor: statusBorder.unavailable,
  },
  unchecked: mutedStatus,
  stale: mutedStatus,
  unconfigured: mutedStatus,
}

const uncheckedStatus: StatusMeta = mutedStatus

const visualStatus = computed(() => displayStatus(props.model))
const status = computed(() => statusMeta[visualStatus.value] ?? uncheckedStatus)

const rateLabel = computed(() => props.model.probe_count > props.model.skipped_count
  ? `${props.model.success_rate.toFixed(1)}%`
  : '0.0%')

const titleId = computed(() => `model-health-title-${props.model.model_id}`)

// ── 纯扁平快捷复制 ──
const copied = ref(false)
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function handleCopy() {
  const textToCopy = props.model.public_id || props.model.display_name || String(props.model.model_id)
  try {
    if (globalThis.navigator?.clipboard?.writeText) {
      await globalThis.navigator.clipboard.writeText(textToCopy)
    } else {
      const textarea = globalThis.document.createElement('textarea')
      textarea.value = textToCopy
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      globalThis.document.body.appendChild(textarea)
      textarea.select()
      globalThis.document.execCommand('copy')
      globalThis.document.body.removeChild(textarea)
    }
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copied.value = false
    }, 1500)
  } catch {
    // 降级静默
  }
}

// ── 时间线悬停跟随 Tooltip ──
const hoveredBucket = ref<ModelHealthBucket | null>(null)
const hoveredIndex = ref<number | null>(null)
const tooltipX = ref(0)
const tooltipPlacement = ref<HorizontalPlacement>('center')
const timelineTrackRef = ref<globalThis.HTMLElement | null>(null)

function handleBucketHover(bucket: ModelHealthBucket, index: number, event: globalThis.MouseEvent) {
  hoveredBucket.value = bucket
  hoveredIndex.value = index
  if (timelineTrackRef.value) {
    const rect = timelineTrackRef.value.getBoundingClientRect()
    const rawX = event.clientX - rect.left
    tooltipX.value = Math.max(8, Math.min(Math.max(8, rect.width - 8), rawX))
    tooltipPlacement.value = horizontalPlacement(rawX, rect.width)
  }
}

function handleTrackLeave() {
  hoveredBucket.value = null
  hoveredIndex.value = null
}

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

// 时间线条形色：与状态 token 同源，hover 只降不透明度，不换色相。
function outcomeBarStyle(outcome: ModelHealthOutcome, isHovered: boolean): { backgroundColor: string } {
  const tone = (token: string) => ({
    backgroundColor: isHovered
      ? `color-mix(in srgb, ${token} 72%, var(--color-surface))`
      : token,
  })
  switch (outcome) {
    case 'success':
      return tone('var(--color-success)')
    case 'failure':
      return tone('var(--color-danger)')
    case 'timeout':
    case 'mixed':
      return tone('var(--color-warning)')
    case 'skipped':
    case 'canceled':
    case 'empty':
    default:
      return { backgroundColor: isHovered ? 'var(--color-border-strong)' : 'var(--color-muted-border)' }
  }
}

function outcomeColor(outcome: ModelHealthOutcome): string {
  switch (outcome) {
    case 'success': return 'var(--color-success)'
    case 'failure': return 'var(--color-danger)'
    case 'timeout':
    case 'mixed': return 'var(--color-warning)'
    default: return 'var(--color-text-subtle)'
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

function formatScaleTime(dateStr?: string): string {
  if (!dateStr) return '—'
  const d = new Date(dateStr)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })
}

function bucketTitle(bucket: ModelHealthBucket): string {
  return `${formatDate(bucket.start, { seconds: true })} · ${outcomeLabel(bucket.outcome)} · ${bucket.probe_count} 次`
}

function timelineLabel(model: ModelHealthModel): string {
  return `${model.display_name || model.public_id} 的 ${model.buckets.length} 个时间段探测状态；当前${status.value.label}`
}

function providerLabel(provider: string): string {
  if (!provider) return '未知'
  if (provider === 'opencodefree') return 'OpenCodeFree'
  if (provider === 'nvidia') return 'NVIDIA NIM'
  return provider.toUpperCase()
}
</script>

<template>
  <article
    class="model-health-card relative min-w-0 flex flex-col justify-between overflow-visible rounded-[var(--radius-panel)] border bg-[var(--color-surface)] p-4 transition-colors duration-[var(--duration-micro)] sm:p-5"
    :class="status.borderColor"
    :data-testid="`model-health-card-${model.model_id}`"
    :data-status="visualStatus"
    :aria-labelledby="titleId"
  >
    <div>
      <!-- 头部第一行：状态徽章 + 渠道/类型标签 + 快捷复制 -->
      <div class="flex items-start justify-between gap-2">
        <div class="flex min-w-0 flex-wrap items-center gap-1.5">
          <!-- 状态徽章：复用全站 badge 词汇，避免这里再拼一套配色 -->
          <span
            :class="{
              'badge-success': status.variant === 'success',
              'badge-warning': status.variant === 'warning',
              'badge-danger': status.variant === 'danger',
              'badge-muted': status.variant === 'muted',
            }"
          >
            <span
              class="h-1.5 w-1.5 shrink-0 rounded-full"
              :style="{ backgroundColor: status.dotColor }"
              aria-hidden="true"
            />
            <span>{{ status.label }}</span>
          </span>

          <!-- 渠道标签 -->
          <span class="rounded-[var(--radius-control)] border border-[var(--color-border-subtle)] bg-[var(--color-sunken)] px-1.5 py-0.5 text-xs font-mono-data uppercase tracking-[0.03em] text-[var(--color-text-secondary)]">
            {{ providerLabel(model.provider) }}
          </span>

          <!-- 类型标签 -->
          <span
            v-if="model.kind"
            class="rounded-[var(--radius-control)] bg-[var(--color-sunken)] px-1.5 py-0.5 text-xs uppercase tracking-[0.03em] text-[var(--color-text-subtle)]"
          >
            {{ model.kind }}
          </span>

          <!-- 停用标签 -->
          <span
            v-if="!model.enabled"
            class="rounded-[var(--radius-control)] border border-[var(--color-disabled-border)] bg-[var(--color-disabled-background)] px-1.5 py-0.5 text-xs font-medium text-[var(--color-disabled-foreground)]"
          >
            停用
          </span>
        </div>

        <!-- 快捷复制：扁平 ghost，命中区不低于 24px -->
        <button
          type="button"
          class="inline-flex min-h-6 shrink-0 items-center gap-1 rounded-[var(--radius-control)] px-1.5 text-xs font-medium transition-colors duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:translate-y-px focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:min-h-11"
          :class="copied ? 'text-[var(--color-success-text)]' : 'text-[var(--color-text-muted)]'"
          :title="`复制模型 ID: ${model.public_id || model.display_name}`"
          @click="handleCopy"
        >
          <UiIcon
            :name="copied ? 'check' : 'copy'"
            :size="13"
            class="shrink-0"
          />
          <span>{{ copied ? '已复制' : '复制 ID' }}</span>
        </button>
      </div>

      <!-- 头部第二行：模型标题 -->
      <div class="mt-2.5">
        <h2
          :id="titleId"
          class="m-0 truncate font-mono-data text-sm font-semibold text-[var(--color-text)]"
          :title="model.display_name || model.public_id"
        >
          {{ model.display_name || model.public_id }}
        </h2>
        <p
          v-if="model.display_name && model.display_name !== model.public_id"
          class="m-0 mt-0.5 truncate font-mono-data text-xs text-[var(--color-text-muted)]"
          :title="model.public_id"
        >
          {{ model.public_id }}
        </p>
      </div>

      <!-- 核心指标栏：开放式 4 列，上下细分割线，不做框中框 -->
      <div class="my-3 grid grid-cols-2 gap-x-2 gap-y-3 border-y border-[var(--color-border-subtle)] py-3 sm:grid-cols-4">
        <!-- 可用率 -->
        <div class="min-w-0">
          <span class="block truncate text-xs text-[var(--color-text-muted)]">可用率</span>
          <span
            class="mt-0.5 block truncate font-mono-data text-base font-semibold"
            :class="{
              'text-[var(--color-success-text)]': model.success_rate >= 85 && model.probe_count > 0,
              'text-[var(--color-warning-text)]': model.success_rate < 85 && model.success_rate >= 50 && model.probe_count > 0,
              'text-[var(--color-danger-text)]': model.success_rate < 50 && model.probe_count > 0,
              'text-[var(--color-text)]': model.probe_count === 0,
            }"
            :data-testid="`model-health-rate-${model.model_id}`"
          >
            {{ rateLabel }}
          </span>
        </div>

        <!-- 最新延迟 -->
        <div class="min-w-0">
          <span class="block truncate text-xs text-[var(--color-text-muted)]">最新延迟</span>
          <span class="mt-0.5 block truncate font-mono-data text-base font-semibold text-[var(--color-text)]">
            <template v-if="model.last_duration_ms !== undefined">
              {{ model.last_duration_ms }}<span class="ml-0.5 text-xs font-normal text-[var(--color-text-muted)]">ms</span>
            </template>
            <template v-else>
              —
            </template>
          </span>
        </div>

        <!-- 探测次数 -->
        <div class="min-w-0">
          <span class="block truncate text-xs text-[var(--color-text-muted)]">探测次数</span>
          <span class="mt-0.5 block truncate font-mono-data text-base font-semibold text-[var(--color-text)]">
            {{ model.probe_count }}
          </span>
        </div>

        <!-- 连续失败 -->
        <div class="min-w-0">
          <span class="block truncate text-xs text-[var(--color-text-muted)]">连续失败</span>
          <span
            class="mt-0.5 block truncate font-mono-data text-base font-semibold"
            :class="model.consecutive_failures > 0 ? 'text-[var(--color-danger-text)]' : 'text-[var(--color-text)]'"
          >
            {{ model.consecutive_failures }}
          </span>
        </div>
      </div>

      <!-- 状态时间线 -->
      <div class="relative mt-1">
        <div class="mb-1.5 flex items-center justify-between gap-2 text-xs">
          <span class="font-medium text-[var(--color-text-secondary)]">状态时间线</span>
          <span class="shrink-0 font-mono-data text-[var(--color-text-subtle)]">
            {{ model.buckets.length ? `${model.buckets.length} 个时间段` : '暂无数据' }}
          </span>
        </div>

        <!-- 胶囊轨道 -->
        <div
          ref="timelineTrackRef"
          :data-testid="`model-health-timeline-${model.model_id}`"
          class="relative flex h-6 items-stretch gap-[1.5px] rounded-[var(--radius-control)] bg-[var(--color-sunken)] p-[2px]"
          role="group"
          :aria-label="timelineLabel(model)"
          @mouseleave="handleTrackLeave"
        >
          <!-- 跟随 Tooltip -->
          <div
            v-if="hoveredBucket"
            data-testid="model-health-tooltip"
            class="model-health-tooltip pointer-events-none absolute bottom-[calc(100%+6px)] z-[var(--z-tooltip)] flex flex-col items-center whitespace-normal rounded-[var(--radius-control)] border border-[var(--color-border-strong)] bg-[var(--color-surface-raised)] px-2.5 py-1.5"
            :class="tooltipPlacement === 'start' ? 'translate-x-0' : tooltipPlacement === 'end' ? '-translate-x-full' : '-translate-x-1/2'"
            :style="{ left: `${tooltipX}px` }"
          >
            <div class="flex items-center gap-1.5 text-xs font-semibold text-[var(--color-text)]">
              <span
                class="inline-block h-2 w-2 shrink-0 rounded-full"
                :style="{ backgroundColor: outcomeColor(hoveredBucket.outcome) }"
              />
              <span>{{ outcomeLabel(hoveredBucket.outcome) }}</span>
              <span class="font-normal text-[var(--color-text-muted)]">· {{ hoveredBucket.probe_count }} 次探测</span>
            </div>
            <div class="mt-0.5 flex items-center gap-2 font-mono-data text-xs text-[var(--color-text-muted)]">
              <span>{{ formatDate(hoveredBucket.start, { seconds: true }) }}</span>
              <span v-if="hoveredBucket.average_duration_ms > 0">{{ hoveredBucket.average_duration_ms }}ms</span>
            </div>
            <!-- 指向宿主格子的小三角 -->
            <div
              class="absolute -bottom-1 h-1.5 w-1.5 rotate-45 border-b border-r border-[var(--color-border-strong)] bg-[var(--color-surface-raised)]"
            />
          </div>

          <span
            v-if="model.buckets.length === 0"
            class="min-w-0 flex-1 rounded-[var(--radius-data)] border border-dashed border-[var(--color-muted-border)]"
            aria-hidden="true"
          />

          <span
            v-for="(bucket, index) in model.buckets"
            :key="`${bucket.start}-${index}`"
            :data-testid="`model-health-bucket-${model.model_id}-${index}`"
            class="min-w-0 flex-1 cursor-pointer rounded-[1px] transition-colors duration-[var(--duration-micro)]"
            :style="outcomeBarStyle(bucket.outcome, hoveredIndex === index)"
            :title="bucketTitle(bucket)"
            :aria-label="bucketTitle(bucket)"
            role="img"
            @mouseenter="handleBucketHover(bucket, index, $event)"
            @mousemove="handleBucketHover(bucket, index, $event)"
          />
        </div>

        <!-- 时间刻度 -->
        <div class="mt-1.5 flex justify-between gap-2 font-mono-data text-xs text-[var(--color-text-subtle)]">
          <span class="truncate">{{ model.buckets.length ? formatScaleTime(model.buckets[0]?.start) : '—' }}</span>
          <span class="truncate">{{ model.buckets.length > 1 ? formatScaleTime(model.buckets[Math.floor(model.buckets.length / 2)]?.start) : '—' }}</span>
          <span class="truncate text-[var(--color-text-muted)]">现在</span>
        </div>
      </div>
    </div>

    <!-- 底部异常提示条 -->
    <div
      v-if="model.last_error_code"
      class="mt-3 flex min-w-0 items-center gap-1.5 rounded-[var(--radius-control)] border border-[var(--color-danger-background)] bg-[var(--color-danger-background)] px-2.5 py-1.5 text-xs text-[var(--color-danger-foreground)]"
    >
      <UiIcon
        name="warning"
        :size="13"
        class="shrink-0 text-current"
        aria-hidden="true"
      />
      <span class="truncate">最近异常：{{ errorLabel(model.last_error_code) }}</span>
    </div>
  </article>
</template>

<style scoped>
.model-health-tooltip {
  max-width: min(18rem, calc(100% - 1rem), calc(100vw - 2rem));
  overflow-wrap: anywhere;
}
</style>
