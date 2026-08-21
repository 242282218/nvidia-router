<script setup lang="ts">
import { computed } from 'vue'

import { formatChartValue } from './geometry'

// 堆叠柱状图：按日/按桶的多序列占比对比（如 Token 输入/输出）。
// 每根柱带 title 提示与 aria 文案；序列图例由父级渲染或用内置 legend。
defineOptions({ name: 'ChartBars' })

export interface BarSeries {
  key: string
  label: string
  color: string
}

const props = withDefaults(defineProps<{
  labels: readonly string[]
  /** values[seriesIndex][labelIndex] */
  values: readonly (readonly number[])[]
  series: readonly BarSeries[]
  height?: number
  showLegend?: boolean
  ariaLabel: string
}>(), { height: 180, showLegend: true })

const viewBoxWidth = 720
const plotTop = 12
const baselineY = 168

const columnTotals = computed(() => props.labels.map((_, li) =>
  props.values.reduce((sum, seriesValues) => sum + (seriesValues[li] ?? 0), 0),
))

const maxValue = computed(() => Math.max(1, ...columnTotals.value))

const barWidth = computed(() => {
  const count = props.labels.length
  if (count === 0) return 0
  const slot = viewBoxWidth / count
  return Math.min(slot * 0.62, 42)
})

function xCenter(labelIndex: number): number {
  const slot = viewBoxWidth / Math.max(props.labels.length, 1)
  return slot * labelIndex + slot / 2
}

interface StackRect { seriesIndex: number, x: number, y: number, w: number, h: number, value: number, label: string }

const stacks = computed<StackRect[][]>(() => props.labels.map((label, li) => {
  let consumed = 0
  const rects: StackRect[] = []
  props.series.forEach((s, si) => {
    const value = props.values[si]?.[li] ?? 0
    if (value <= 0) return
    const hFull = (value / maxValue.value) * (baselineY - plotTop)
    rects.push({
      seriesIndex: si,
      x: xCenter(li) - barWidth.value / 2,
      y: baselineY - consumed - hFull,
      w: barWidth.value,
      h: Math.max(hFull, 1.5),
      value,
      label,
    })
    consumed += hFull
  })
  return rects
}))

const flatStacks = computed(() => stacks.value.flat())
</script>

<template>
  <div>
    <svg
      class="w-full"
      :style="{ height: `${height}px` }"
      :viewBox="`0 0 ${viewBoxWidth} ${baselineY + 4}`"
      preserveAspectRatio="none"
      role="img"
      :aria-label="ariaLabel"
    >
      <line
        x1="0"
        :y1="baselineY"
        :x2="viewBoxWidth"
        :y2="baselineY"
        stroke="var(--color-border-strong)"
        stroke-width="1"
        vector-effect="non-scaling-stroke"
      />
      <g
        v-for="(rects, li) in stacks"
        :key="li"
      >
        <rect
          v-for="rect in rects"
          :key="`${li}-${rect.seriesIndex}`"
          :x="rect.x"
          :y="rect.y"
          :width="rect.w"
          :height="rect.h"
          rx="3"
          :fill="series[rect.seriesIndex]?.color"
          class="transition-[y,height] duration-[var(--duration-local)]"
        >
          <title>{{ `${rect.label} · ${series[rect.seriesIndex]?.label}：${formatChartValue(rect.value)}` }}</title>
        </rect>
      </g>
    </svg>

    <div
      v-if="showLegend"
      class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1"
    >
      <span
        v-for="s in series"
        :key="s.key"
        class="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]"
      >
        <span
          class="h-2.5 w-2.5 rounded-full"
          :style="{ background: s.color }"
          aria-hidden="true"
        />
        {{ s.label }}
      </span>
      <span class="font-mono-data ml-auto text-xs text-[var(--color-text-subtle)]">
        {{ formatChartValue(columnTotals.reduce((a, b) => a + b, 0)) }} 总计
      </span>
    </div>
    <span class="sr-only">{{ flatStacks.length }} 根柱，{{ labels.length }} 组</span>
  </div>
</template>
