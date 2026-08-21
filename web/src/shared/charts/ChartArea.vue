<script setup lang="ts">
import { computed, ref } from 'vue'

import { formatChartValue, niceMidpoint, smoothPath, type Point } from './geometry'

// 通用面积/折线图：由 MonitoringTrendChart 泛化而来。
// 语义色由调用方以 CSS 变量传入（数据可视化红线：每条序列一个语义色），
// hover 十字线吸附最近点，tooltip 用 HTML 覆盖层渲染。
defineOptions({ name: 'ChartArea' })

const props = withDefaults(defineProps<{
  /** 数据点：label 为 X 轴时间桶文案。 */
  points: readonly { label: string, value: number }[]
  /** 线与渐变的颜色（CSS 变量或色值）。 */
  color: string
  /** 虚线第二编码（色盲冗余），如失败趋势。 */
  dashed?: boolean
  ariaLabel: string
  /** 隐藏 Y 轴刻度（sparkline 场景外一般保留）。 */
  showAxis?: boolean
}>(), {
  dashed: false,
  showAxis: true,
})

const plotLeft = 48
const viewBoxWidth = 720
const baselineY = 188
const topY = 28
const plotWidth = viewBoxWidth - plotLeft

const maxValue = computed(() => Math.max(1, ...props.points.map((p) => p.value)))

function xAt(index: number): number {
  const total = props.points.length
  if (total <= 1) return plotLeft + plotWidth / 2
  return plotLeft + (index / (total - 1)) * plotWidth
}

function yAt(value: number): number {
  return baselineY - (value / maxValue.value) * (baselineY - topY)
}

const linePath = computed(() => {
  const pts: Point[] = props.points.map((p, i) => ({ x: xAt(i), y: yAt(p.value) }))
  return smoothPath(pts)
})

const areaPath = computed(() => {
  if (!linePath.value || props.points.length === 0) return ''
  const lastX = xAt(props.points.length - 1).toFixed(1)
  const firstX = xAt(0).toFixed(1)
  return `${linePath.value} L${lastX},${baselineY} L${firstX},${baselineY} Z`
})

// SVG defs 渐变 id 必须实例唯一，否则多图同页时 url(#) 串引用
const gradientId = `chart-area-${Math.random().toString(36).slice(2, 10)}`

const hoverIndex = ref<number | null>(null)

function onPointerMove(event: globalThis.PointerEvent): void {
  const target = event.currentTarget as globalThis.HTMLElement
  const rect = target.getBoundingClientRect()
  if (rect.width === 0 || props.points.length === 0) return
  const svgX = ((event.clientX - rect.left) / rect.width) * viewBoxWidth
  const ratio = Math.min(1, Math.max(0, (svgX - plotLeft) / plotWidth))
  hoverIndex.value = Math.round(ratio * (props.points.length - 1))
}

function onPointerLeave(): void {
  hoverIndex.value = null
}

const hoverPoint = computed(() => {
  const index = hoverIndex.value
  const point = index === null ? undefined : props.points[index]
  if (index === null || point === undefined) return null
  return { index, x: xAt(index), y: yAt(point.value), point }
})

const hoverStyle = computed(() => {
  const hover = hoverPoint.value
  if (!hover) return null
  const percent = (hover.x / viewBoxWidth) * 100
  const flip = percent > 78 ? '-translate-x-full' : (percent < 22 ? 'translate-x-0' : '-translate-x-1/2')
  return { left: `${percent}%`, class: flip }
})

const yAxisLabels = computed(() => [
  { y: baselineY, value: 0 },
  { y: (baselineY + topY) / 2, value: niceMidpoint(maxValue.value) },
  { y: topY, value: maxValue.value },
])
</script>

<template>
  <div class="relative">
    <svg
      class="h-48 w-full touch-none"
      :class="showAxis ? '' : 'h-full'"
      viewBox="0 0 720 220"
      role="img"
      :aria-label="ariaLabel"
      @pointermove="onPointerMove"
      @pointerdown="onPointerMove"
      @pointerleave="onPointerLeave"
    >
      <defs>
        <linearGradient
          :id="gradientId"
          x1="0"
          y1="0"
          x2="0"
          y2="1"
        >
          <!-- Warm Restraint：面积淡到 8%，让线条本身承担表达 -->
          <stop
            offset="0%"
            :stop-color="color"
            stop-opacity="0.08"
          />
          <stop
            offset="100%"
            :stop-color="color"
            stop-opacity="0.02"
          />
        </linearGradient>
      </defs>
      <template v-if="showAxis">
        <!-- 仅一条基准线（Warm Restraint：去掉全部虚线网格） -->
        <line
          :x1="plotLeft"
          :y1="baselineY"
          :x2="viewBoxWidth"
          :y2="baselineY"
          stroke="var(--color-border)"
          stroke-width="1"
        />
        <text
          v-for="label in yAxisLabels"
          :key="`t${label.y}`"
          :x="plotLeft - 8"
          :y="label.y + 4"
          class="fill-[var(--color-text-muted)]"
          font-size="11"
          text-anchor="end"
          font-family="ui-monospace, monospace"
        >
          {{ formatChartValue(label.value) }}
        </text>
      </template>
      <path
        :d="areaPath"
        :fill="`url(#${gradientId})`"
        class="chart-area-fade"
      />
      <path
        :d="linePath"
        fill="none"
        :stroke="color"
        class="chart-line-draw"
        :class="{ 'chart-line-static': dashed }"
        :pathLength="dashed ? undefined : 1"
        :stroke-dasharray="dashed ? '6 4' : undefined"
        stroke-width="1.25"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <g v-if="hoverPoint">
        <line
          :x1="hoverPoint.x"
          :y1="topY - 8"
          :x2="hoverPoint.x"
          :y2="baselineY"
          stroke="var(--color-border-strong)"
          stroke-width="1"
          stroke-dasharray="3 4"
        />
        <circle
          :cx="hoverPoint.x"
          :cy="hoverPoint.y"
          r="3.5"
          fill="var(--color-elevated)"
          :stroke="color"
          stroke-width="2"
        />
      </g>
    </svg>

    <div
      v-if="hoverPoint && hoverStyle"
      class="pointer-events-none absolute top-6 z-10 min-w-32 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-3 py-2 shadow-[var(--shadow-md)]"
      :class="hoverStyle.class"
      :style="{ left: hoverStyle.left }"
      role="status"
    >
      <p class="font-mono-data text-[11px] text-[var(--color-text-muted)]">
        {{ hoverPoint.point.label }}
      </p>
      <p
        class="font-mono-data mt-0.5 text-sm font-semibold"
        :style="{ color }"
      >
        {{ formatChartValue(hoverPoint.point.value) }}
      </p>
    </div>
  </div>
</template>

<style scoped>
/* Warm Restraint 数据仪式：线条从左到右生长 700ms，面积同步淡入。
   pathLength=1 归一化周长，dashoffset 1→0 即完整描画一次；
   虚线（失败趋势）依赖 dasharray 表达第二编码，不参与生长动画。
   reduced-motion 由全局 CSS 把动画压到瞬时，无需在此重复。 */
.chart-line-draw {
  stroke-dasharray: 1;
  animation: chart-line-draw 700ms var(--ease-enter) both;
}
.chart-line-draw.chart-line-static {
  animation: none;
  stroke-dasharray: 6 4;
}
.chart-area-fade {
  opacity: 0;
  animation: chart-area-fade 500ms var(--ease-enter) 120ms both;
}
@keyframes chart-line-draw {
  from { stroke-dashoffset: 1; }
  to { stroke-dashoffset: 0; }
}
@keyframes chart-area-fade {
  from { opacity: 0; }
  to { opacity: 1; }
}
</style>
