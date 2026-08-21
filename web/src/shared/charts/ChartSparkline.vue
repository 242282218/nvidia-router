<script setup lang="ts">
import { computed } from 'vue'

// 迷你趋势线：归一化 100×28 视窗，preserveAspectRatio=none 拉伸铺满，
// vector-effect 保证描边不随拉伸变粗。从 UiStat 抽出供全站复用。
defineOptions({ name: 'ChartSparkline' })

const props = withDefaults(defineProps<{
  values: readonly number[]
  color?: string
}>(), { color: 'var(--color-info)' })

const SPARK_W = 100
const SPARK_H = 28

const sparkPoints = computed(() => {
  const series = props.values
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
const gradientId = `chart-spark-${Math.random().toString(36).slice(2, 10)}`
</script>

<template>
  <svg
    v-if="sparkAreaPath"
    class="h-7 w-full"
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
          :stop-color="color"
          stop-opacity="0.22"
        />
        <stop
          offset="100%"
          :stop-color="color"
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
      :stroke="color"
      stroke-width="1.5"
      vector-effect="non-scaling-stroke"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
