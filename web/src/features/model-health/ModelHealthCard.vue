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

const statusMeta: Record<string, StatusMeta> = {
  healthy: {
    label: '健康',
    variant: 'success',
    color: 'var(--color-success)',
    dotColor: '#10b981',
    borderColor: 'border-emerald-200/80 dark:border-emerald-500/20 hover:border-emerald-300 dark:hover:border-emerald-500/40',
  },
  degraded: {
    label: '降级',
    variant: 'warning',
    color: 'var(--color-warning)',
    dotColor: '#f59e0b',
    borderColor: 'border-amber-200/80 dark:border-amber-500/20 hover:border-amber-300 dark:hover:border-amber-500/40',
  },
  unavailable: {
    label: '异常',
    variant: 'danger',
    color: 'var(--color-danger)',
    dotColor: '#f43f5e',
    borderColor: 'border-rose-200/90 dark:border-rose-500/30 hover:border-rose-300 dark:hover:border-rose-500/50',
  },
  unchecked: {
    label: '无数据',
    variant: 'muted',
    color: 'var(--color-text-subtle)',
    dotColor: '#94a3b8',
    borderColor: 'border-[var(--color-border-subtle)] hover:border-[var(--color-border-strong)]',
  },
  stale: {
    label: '无数据',
    variant: 'muted',
    color: 'var(--color-text-subtle)',
    dotColor: '#94a3b8',
    borderColor: 'border-[var(--color-border-subtle)] hover:border-[var(--color-border-strong)]',
  },
  unconfigured: {
    label: '无数据',
    variant: 'muted',
    color: 'var(--color-text-subtle)',
    dotColor: '#94a3b8',
    borderColor: 'border-[var(--color-border-subtle)] hover:border-[var(--color-border-strong)]',
  },
}

const uncheckedStatus: StatusMeta = {
  label: '无数据',
  variant: 'muted',
  color: 'var(--color-text-subtle)',
  dotColor: '#94a3b8',
  borderColor: 'border-[var(--color-border-subtle)] hover:border-[var(--color-border-strong)]',
}

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

// 高饱和纯净色彩（现代云原生监控风格）
function outcomeBarClass(outcome: ModelHealthOutcome, isHovered: boolean): string {
  switch (outcome) {
    case 'success':
      return isHovered ? 'bg-emerald-400 dark:bg-emerald-400' : 'bg-emerald-500 dark:bg-emerald-500'
    case 'failure':
      return isHovered ? 'bg-rose-400 dark:bg-rose-400' : 'bg-rose-500 dark:bg-rose-500'
    case 'timeout':
    case 'mixed':
      return isHovered ? 'bg-amber-300 dark:bg-amber-300' : 'bg-amber-400 dark:bg-amber-400'
    case 'skipped':
    case 'canceled':
    case 'empty':
    default:
      return isHovered ? 'bg-zinc-300 dark:bg-zinc-600' : 'bg-zinc-200/80 dark:bg-zinc-800'
  }
}

function outcomeColor(outcome: ModelHealthOutcome): string {
  switch (outcome) {
    case 'success': return '#10b981'
    case 'failure': return '#f43f5e'
    case 'timeout':
    case 'mixed': return '#f59e0b'
    default: return '#9ca3af'
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
  return provider === 'opencodefree' ? 'OpenCodeFree' : provider.toUpperCase()
}
</script>

<template>
  <article
    class="model-health-card relative min-w-0 flex flex-col justify-between overflow-hidden rounded-2xl border bg-[var(--color-surface)] p-4 transition-colors duration-200 sm:p-4.5"
    :class="status.borderColor"
    :data-testid="`model-health-card-${model.model_id}`"
    :data-status="visualStatus"
    :aria-labelledby="titleId"
  >
    <div>
      <!-- 头部第一行：状态 Badge + 提供商/类型标签 + 纯扁平快捷复制 -->
      <div class="flex items-center justify-between gap-2">
        <div class="flex min-w-0 flex-wrap items-center gap-1.5">
          <!-- 状态徽章 (呼吸指示点) -->
          <span
            class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-semibold tracking-wide transition-colors"
            :class="{
              'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400': status.variant === 'success',
              'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-400': status.variant === 'warning',
              'bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-400': status.variant === 'danger',
              'bg-slate-100 text-slate-600 dark:bg-zinc-800 dark:text-zinc-400': status.variant === 'muted',
            }"
          >
            <span
              class="relative flex h-2 w-2"
              aria-hidden="true"
            >
              <span
                class="relative inline-flex h-2 w-2 rounded-full"
                :style="{ backgroundColor: status.dotColor }"
              />
            </span>
            <span>{{ status.label }}</span>
          </span>

          <!-- 提供商 Badge -->
          <span class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-slate-700 dark:bg-zinc-800 dark:text-zinc-300">
            {{ providerLabel(model.provider) }}
          </span>

          <!-- 类型 Tag -->
          <span
            v-if="model.kind"
            class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-slate-500 dark:bg-zinc-800/60 dark:text-zinc-400"
          >
            {{ model.kind }}
          </span>

          <!-- 停用 Tag -->
          <span
            v-if="!model.enabled"
            class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium text-slate-400 dark:bg-zinc-800/40 dark:text-zinc-500"
          >
            停用
          </span>
        </div>

        <!-- 极简扁平 Ghost 复制按钮 -->
        <button
          type="button"
          class="inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium text-[var(--color-text-muted)] transition-colors hover:bg-slate-100 hover:text-[var(--color-text)] active:scale-95 dark:hover:bg-zinc-800"
          :title="`复制模型 ID: ${model.public_id || model.display_name}`"
          @click="handleCopy"
        >
          <UiIcon
            :name="copied ? 'check' : 'copy'"
            :size="12"
            :class="copied ? 'text-emerald-500' : 'text-[var(--color-text-muted)]'"
          />
          <span :class="copied ? 'font-semibold text-emerald-600 dark:text-emerald-400' : ''">
            {{ copied ? '已复制' : '复制 ID' }}
          </span>
        </button>
      </div>

      <!-- 头部第二行：模型标题 -->
      <div class="mt-2.5">
        <h2
          :id="titleId"
          class="m-0 truncate font-mono text-[14px] font-bold tracking-tight text-[var(--color-text)]"
          :title="model.display_name || model.public_id"
        >
          {{ model.display_name || model.public_id }}
        </h2>
        <p
          v-if="model.display_name && model.display_name !== model.public_id"
          class="m-0 truncate font-mono text-[11px] text-[var(--color-text-muted)]"
          :title="model.public_id"
        >
          {{ model.public_id }}
        </p>
      </div>

      <!-- 核心指标栏 (消除框中框：开放式 4 列排版，上下细分割线) -->
      <div class="my-3 grid grid-cols-4 gap-2 border-y border-slate-100 py-2.5 dark:border-zinc-800/80">
        <!-- 成功率 -->
        <div class="min-w-0">
          <span class="block truncate text-[11px] font-medium text-[var(--color-text-muted)]">可用率</span>
          <span
            class="mt-0.5 block truncate font-mono text-[16px] font-bold tracking-tight"
            :class="{
              'text-emerald-600 dark:text-emerald-400': model.success_rate >= 85 && model.probe_count > 0,
              'text-amber-600 dark:text-amber-400': model.success_rate < 85 && model.success_rate >= 50 && model.probe_count > 0,
              'text-rose-600 dark:text-rose-400': model.success_rate < 50 && model.probe_count > 0,
              'text-[var(--color-text)]': model.probe_count === 0,
            }"
            :data-testid="`model-health-rate-${model.model_id}`"
          >
            {{ rateLabel }}
          </span>
        </div>

        <!-- 最近延迟 -->
        <div class="min-w-0">
          <span class="block truncate text-[11px] font-medium text-[var(--color-text-muted)]">最新延迟</span>
          <span class="mt-0.5 block truncate font-mono text-[16px] font-bold tracking-tight text-[var(--color-text)]">
            <template v-if="model.last_duration_ms !== undefined">
              {{ model.last_duration_ms }}<span class="ml-0.5 text-[11px] font-normal text-[var(--color-text-muted)]">ms</span>
            </template>
            <template v-else>
              —
            </template>
          </span>
        </div>

        <!-- 探测总数 -->
        <div class="min-w-0">
          <span class="block truncate text-[11px] font-medium text-[var(--color-text-muted)]">探测次数</span>
          <span class="mt-0.5 block truncate font-mono text-[16px] font-bold tracking-tight text-[var(--color-text)]">
            {{ model.probe_count }}
          </span>
        </div>

        <!-- 连续失败 -->
        <div class="min-w-0">
          <span class="block truncate text-[11px] font-medium text-[var(--color-text-muted)]">连续失败</span>
          <span
            class="mt-0.5 block truncate font-mono text-[16px] font-bold tracking-tight"
            :class="model.consecutive_failures > 0 ? 'text-rose-600 dark:text-rose-400' : 'text-[var(--color-text)]'"
          >
            {{ model.consecutive_failures }}
          </span>
        </div>
      </div>

      <!-- 状态时间线 (纯净条形码轨道与微交互) -->
      <div class="relative mt-1">
        <div class="mb-1 flex items-center justify-between text-[11px]">
          <span class="font-medium text-[var(--color-text-secondary)]">状态时间线</span>
          <span class="font-mono text-[10px] text-[var(--color-text-subtle)]">
            {{ model.buckets.length ? `${model.buckets.length} 个时间段` : '暂无数据' }}
          </span>
        </div>

        <!-- 胶囊轨道 -->
        <div
          ref="timelineTrackRef"
          :data-testid="`model-health-timeline-${model.model_id}`"
          class="relative flex h-5.5 items-stretch gap-[1.5px] rounded-md bg-slate-100/90 p-[2px] dark:bg-zinc-800/80"
          role="group"
          :aria-label="timelineLabel(model)"
          @mouseleave="handleTrackLeave"
        >
          <!-- 跟随 Tooltip -->
          <div
            v-if="hoveredBucket"
            data-testid="model-health-tooltip"
            class="model-health-tooltip pointer-events-none absolute bottom-[calc(100%+6px)] z-30 flex flex-col items-center whitespace-normal rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-surface)] px-2.5 py-1.5 transition-transform duration-75 ease-out"
            :class="tooltipPlacement === 'start' ? 'translate-x-0' : tooltipPlacement === 'end' ? '-translate-x-full' : '-translate-x-1/2'"
            :style="{ left: `${tooltipX}px` }"
          >
            <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--color-text)]">
              <span
                class="inline-block h-2 w-2 rounded-full"
                :style="{ backgroundColor: outcomeColor(hoveredBucket.outcome) }"
              />
              <span>{{ outcomeLabel(hoveredBucket.outcome) }}</span>
              <span class="text-[var(--color-text-muted)]">· {{ hoveredBucket.probe_count }} 次探测</span>
            </div>
            <div class="mt-0.5 flex items-center gap-2 font-mono text-[10px] text-[var(--color-text-muted)]">
              <span>{{ formatDate(hoveredBucket.start, { seconds: true }) }}</span>
              <span v-if="hoveredBucket.average_duration_ms > 0">⚡ {{ hoveredBucket.average_duration_ms }}ms</span>
            </div>
            <!-- 小三角箭头 -->
            <div
              class="absolute -bottom-1 h-1.5 w-1.5 rotate-45 border-b border-r border-[var(--color-border-strong)] bg-[var(--color-surface)]"
            />
          </div>

          <span
            v-if="model.buckets.length === 0"
            class="min-w-0 flex-1 rounded-[1.5px] border border-dashed border-slate-300 dark:border-zinc-700"
            aria-hidden="true"
          />

          <span
            v-for="(bucket, index) in model.buckets"
            :key="`${bucket.start}-${index}`"
            :data-testid="`model-health-bucket-${model.model_id}-${index}`"
            class="min-w-0 flex-1 cursor-pointer rounded-[1px] transition-colors duration-100"
            :class="outcomeBarClass(bucket.outcome, hoveredIndex === index)"
            :title="bucketTitle(bucket)"
            :aria-label="bucketTitle(bucket)"
            role="img"
            @mouseenter="handleBucketHover(bucket, index, $event)"
            @mousemove="handleBucketHover(bucket, index, $event)"
          />
        </div>

        <!-- 时间刻度 (HH:mm) -->
        <div class="mt-1 flex justify-between font-mono text-[10px] text-[var(--color-text-subtle)]">
          <span>{{ model.buckets.length ? formatScaleTime(model.buckets[0]?.start) : '—' }}</span>
          <span>{{ model.buckets.length > 1 ? formatScaleTime(model.buckets[Math.floor(model.buckets.length / 2)]?.start) : '—' }}</span>
          <span class="font-medium text-[var(--color-text-muted)]">现在</span>
        </div>
      </div>
    </div>

    <!-- 底部异常警示条 (仅在有异常时优雅展示) -->
    <div
      v-if="model.last_error_code"
      class="mt-3 flex items-center justify-between gap-2 rounded-lg border border-rose-200/80 bg-rose-50/70 px-2.5 py-1.5 text-xs text-rose-700 dark:border-rose-400/20 dark:bg-rose-400/10 dark:text-rose-400"
    >
      <div class="flex items-center gap-1.5 min-w-0">
        <UiIcon
          name="warning"
          :size="13"
          class="shrink-0 text-current"
          aria-hidden="true"
        />
        <span class="truncate font-mono">最近异常：{{ errorLabel(model.last_error_code) }}</span>
      </div>
    </div>
  </article>
</template>

<style scoped>
.model-health-tooltip {
  max-width: min(18rem, calc(100% - 1rem), calc(100vw - 2rem));
  overflow-wrap: anywhere;
}
</style>
