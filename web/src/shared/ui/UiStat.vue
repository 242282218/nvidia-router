<script setup lang="ts">
import { computed } from 'vue'

import { useCountUp } from '../useCountUp'

// 指标块：label + 大数字 + 补充说明 + 可选迷你趋势。只读遥测，无 hover 反馈。
// tone 仅在指标自带语义时使用（成功率/失败数等），默认中性墨色。
defineOptions({ name: 'UiStat' })

const props = withDefaults(defineProps<{
  label: string
  value: string | number
  hint?: string
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
  /** 等宽数字字体，便于纵向对齐比较。 */
  mono?: boolean
  /** 迷你趋势序列（与指标同窗口）；提供时在卡片底部渲染 sparkline。 */
  sparkline?: number[]
  /** 数值型 value 的格式化函数；缺省千分位整数。 */
  format?: (n: number) => string
}>(), { hint: undefined, tone: 'default', mono: true, sparkline: undefined, format: undefined })

const toneClass: Record<string, string> = {
  default: 'text-[var(--color-text)]',
  success: 'text-[var(--color-success)]',
  warning: 'text-[var(--color-warning)]',
  danger: 'text-[var(--color-danger)]',
  info: 'text-[var(--color-info)]',
}

const toneColorVar: Record<string, string> = {
  default: 'var(--color-info)',
  success: 'var(--color-success)',
  warning: 'var(--color-warning)',
  danger: 'var(--color-danger)',
  info: 'var(--color-info)',
}

const integerFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 })

// 数值型 value 走 count-up；字符串（预格式化）原样展示。
const numericTarget = computed(() => (typeof props.value === 'number' ? props.value : 0))
const animated = useCountUp(numericTarget)

const displayValue = computed(() => {
  if (typeof props.value === 'string') return props.value
  if (props.format) return props.format(animated.value)
  return integerFormat.format(Math.round(animated.value))
})

// ── sparkline 几何：归一化到 100×28 视窗，preserveAspectRatio=none 拉伸铺满，
// vector-effect 保证描边不随拉伸变粗。 ──
const SPARK_W = 100
const SPARK_H = 28

const sparkPoints = computed(() => {
  const series = props.sparkline ?? []
  if (series.length < 2) return []
  const max = Math.max(...series)
  const min = Math.min(...series)
  const span = max - min || 1
  return series.map((value, index) => ({
    x: (index / (series.length - 1)) * SPARK_W,
    y: SPARK_H - 3 - ((value - min) / span) * (SPARK_H - 6),
  }))
})

const sparkLinePath = computed(() => sparkPoints.value
  .map((point, index) => `${index === 0 ? 'M' : 'L'}${point.x.toFixed(2)},${point.y.toFixed(2)}`)
  .join(' '))

const sparkAreaPath = computed(() => {
  const points = sparkPoints.value
  const first = points[0]
  const last = points[points.length - 1]
  if (!first || !last) return ''
  const baseline = SPARK_H
  return `${sparkLinePath.value} L${last.x.toFixed(2)},${baseline} L${first.x.toFixed(2)},${baseline} Z`
})

// 渐变 id 需实例唯一，避免同页多卡片共享 defs 冲突
const gradientId = `ui-stat-spark-${Math.random().toString(36).slice(2, 10)}`
</script>

<template>
  <div class="metric-card flex flex-col">
    <p class="type-label">
      {{ label }}
    </p>
    <p
      class="mt-2 text-[22px] font-semibold leading-tight"
      :class="[toneClass[tone], mono ? 'font-mono-data' : '']"
    >
      {{ displayValue }}
    </p>
    <p
      v-if="hint"
      class="mt-1 text-xs text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
    <svg
      v-if="sparkAreaPath"
      class="mt-3 h-7 w-full"
      :viewBox="`0 0 ${SPARK_W} ${SPARK_H}`"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <defs>
        <linearGradient
          :id="gradientId"
          x1="0"
          y1="0"
          x2="0"
          y2="1"
        >
          <stop
            offset="0%"
            :stop-color="toneColorVar[tone]"
            stop-opacity="0.22"
          />
          <stop
            offset="100%"
            :stop-color="toneColorVar[tone]"
            stop-opacity="0"
          />
        </linearGradient>
      </defs>
      <path
        :d="sparkAreaPath"
        :fill="`url(#${gradientId})`"
      />
      <path
        :d="sparkLinePath"
        fill="none"
        :stroke="toneColorVar[tone]"
        stroke-width="1.5"
        vector-effect="non-scaling-stroke"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </div>
</template>
