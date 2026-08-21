<script setup lang="ts">
import { computed } from 'vue'

import { formatChartValue } from './geometry'

// 环形分布图：结果分布（success/canceled/failure）等占比可视化。
// 每段必须带文字图例（无障碍红线：不能只靠颜色区分）。
defineOptions({ name: 'ChartDonut' })

export interface DonutSegment {
  label: string
  value: number
  color: string
}

const props = withDefaults(defineProps<{
  segments: readonly DonutSegment[]
  size?: number
  thickness?: number
  /** 中心主文案；缺省显示总值。 */
  centerLabel?: string
}>(), { size: 148, thickness: 16, centerLabel: undefined })

const total = computed(() => props.segments.reduce((sum, s) => sum + s.value, 0))

const radius = computed(() => (props.size - props.thickness) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)

// 每段的 dasharray/offset：沿圆周顺时针排布，段间留 2px 视觉间隙
const arcs = computed(() => {
  let consumed = 0
  return props.segments.map((segment) => {
    const fraction = total.value > 0 ? segment.value / total.value : 0
    const length = circumference.value * fraction
    const arc = {
      segment,
      dasharray: `${Math.max(length - 2, 0)} ${circumference.value - Math.max(length - 2, 0)}`,
      offset: -consumed,
      percent: Math.round(fraction * 100),
    }
    consumed += length
    return arc
  })
})
</script>

<template>
  <div class="flex flex-wrap items-center gap-5">
    <div
      class="relative inline-flex shrink-0 items-center justify-center"
      role="img"
      :aria-label="`分布环图，总计 ${formatChartValue(total)}`"
    >
      <svg
        :width="size"
        :height="size"
        :viewBox="`0 0 ${size} ${size}`"
        fill="none"
      >
        <circle
          :cx="size / 2"
          :cy="size / 2"
          :r="radius"
          stroke="var(--color-border-subtle)"
          :stroke-width="thickness"
        />
        <circle
          v-for="(arc, index) in arcs"
          :key="arc.segment.label"
          :cx="size / 2"
          :cy="size / 2"
          :r="radius"
          :stroke="arc.segment.color"
          :stroke-width="thickness"
          stroke-linecap="butt"
          :stroke-dasharray="arc.dasharray"
          :stroke-dashoffset="arc.offset"
          transform="rotate(-90)"
          :style="{ transformOrigin: `${size / 2}px ${size / 2}px`, transition: `stroke-dasharray var(--duration-overlay) var(--ease-enter) ${index * 60}ms` }"
        />
      </svg>
      <div class="absolute inset-0 flex flex-col items-center justify-center">
        <span class="font-mono-data text-xl font-semibold leading-none text-[var(--color-text)]">
          {{ centerLabel ?? formatChartValue(total) }}
        </span>
        <slot name="sub" />
      </div>
    </div>

    <!-- 文字图例：色盲与读屏的第二编码 -->
    <ul class="min-w-32 flex-1 space-y-1.5">
      <li
        v-for="arc in arcs"
        :key="arc.segment.label"
        class="flex items-center gap-2 text-sm"
      >
        <span
          class="h-2.5 w-2.5 shrink-0 rounded-full"
          :style="{ background: arc.segment.color }"
          aria-hidden="true"
        />
        <span class="text-[var(--color-text-secondary)]">{{ arc.segment.label }}</span>
        <span class="font-mono-data ml-auto text-[var(--color-text)]">{{ formatChartValue(arc.segment.value) }}</span>
        <span class="font-mono-data w-10 text-right text-xs text-[var(--color-text-muted)]">{{ arc.percent }}%</span>
      </li>
    </ul>
  </div>
</template>
