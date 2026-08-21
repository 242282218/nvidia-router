<script setup lang="ts">
import { computed, ref } from 'vue'

import type { MonitoringSeriesPoint } from './types'

type TrendMetric = 'requests' | 'failures' | 'latency' | 'tokens'

const props = defineProps<{
  series: MonitoringSeriesPoint[]
  metric: TrendMetric
  title: string
  /** Human-readable time window, e.g. "24 小时". Shown so the chart is
   * self-explanatory without the page-level range control (design-aesthetics
   * P0#5: source, window and unit must be visible with the data). */
  rangeLabel?: string
}>()

const metricLabel = computed(() => {
  if (props.metric === 'latency') return '平均耗时（毫秒）'
  if (props.metric === 'tokens') return '输入 + 输出 Token'
  if (props.metric === 'failures') return '失败请求数'
  return '请求数'
})

const values = computed(() => props.series.map((point) => {
  if (props.metric === 'latency') return point.average_duration_ms
  if (props.metric === 'tokens') return point.prompt_tokens + point.completion_tokens
  if (props.metric === 'failures') return point.failure_count
  return point.request_count
}))

const maxValue = computed(() => Math.max(1, ...values.value))

// Each metric carries its own semantic colour (design-aesthetics 数据可视化
// P0#3): failures are danger red, latency is warning amber, tokens success
// green and requests the neutral info indigo. A single colour for every chart
// made "failure trend" read like "request trend" at a glance.
const seriesColor = computed(() => {
  switch (props.metric) {
    case 'failures':
      return 'var(--color-danger)'
    case 'latency':
      return 'var(--color-warning)'
    case 'tokens':
      return 'var(--color-success)'
    default:
      return 'var(--color-info)'
  }
})

// Failures additionally use a dashed line as a second encoding, so the series
// stays distinguishable for colour-blind operators who cannot rely on hue.
const lineDash = computed(() => (props.metric === 'failures' ? '6 4' : undefined))

// Plot geometry: a 48px left gutter carries the Y-axis labels so the line chart
// stays readable without shifting the SVG viewBox.
const plotLeft = 48
const viewBoxWidth = 720
const baselineY = 188
const topY = 28
const plotWidth = viewBoxWidth - plotLeft

function xAt(index: number): number {
  const total = values.value.length
  if (total <= 1) return plotLeft + plotWidth / 2
  return plotLeft + (index / (total - 1)) * plotWidth
}

function yAt(value: number): number {
  return baselineY - (value / maxValue.value) * (baselineY - topY)
}

// Catmull-Rom → cubic Bézier：把折线转成平滑曲线，张力 0.5 抑制过冲，
// 数值不会画出 [0, max] 之外（监控图不允许曲线"编造"不存在的峰值）。
const smoothPath = computed(() => {
  const points = values.value.map((value, index) => ({ x: xAt(index), y: yAt(value) }))
  if (points.length === 0) return ''
  if (points.length === 1) {
    const only = points[0]
    return only ? `M${only.x.toFixed(1)},${only.y.toFixed(1)}` : ''
  }
  const firstPoint = points[0]
  if (!firstPoint) return ''
  let path = `M${firstPoint.x.toFixed(1)},${firstPoint.y.toFixed(1)}`
  for (let i = 0; i < points.length - 1; i++) {
    const p1 = points[i]
    const p2 = points[i + 1]
    const p0 = points[Math.max(0, i - 1)]
    const p3 = points[Math.min(points.length - 1, i + 2)]
    if (!p1 || !p2 || !p0 || !p3) break
    const c1x = p1.x + ((p2.x - p0.x) / 6)
    const c1y = p1.y + ((p2.y - p0.y) / 6)
    const c2x = p2.x - ((p3.x - p1.x) / 6)
    const c2y = p2.y - ((p3.y - p1.y) / 6)
    path += ` C${c1x.toFixed(1)},${c1y.toFixed(1)} ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2.x.toFixed(1)},${p2.y.toFixed(1)}`
  }
  return path
})

const areaPath = computed(() => {
  if (!smoothPath.value || values.value.length === 0) return ''
  const lastX = xAt(values.value.length - 1).toFixed(1)
  const firstX = xAt(0).toFixed(1)
  return `${smoothPath.value} L${lastX},${baselineY} L${firstX},${baselineY} Z`
})

const gradientId = `trend-grad-${Math.random().toString(36).slice(2, 10)}`

// ── hover 十字线：pointer 移动时吸附最近数据点，tooltip 用 HTML 覆盖层渲染 ──
const hoverIndex = ref<number | null>(null)

function onPointerMove(event: globalThis.PointerEvent): void {
  const target = event.currentTarget as globalThis.HTMLElement
  const rect = target.getBoundingClientRect()
  if (rect.width === 0) return
  // viewBox 坐标 → 实际像素比例是线性的，直接按宽度换算即可。
  const svgX = ((event.clientX - rect.left) / rect.width) * viewBoxWidth
  const total = values.value.length
  if (total === 0) return
  const ratio = Math.min(1, Math.max(0, (svgX - plotLeft) / plotWidth))
  hoverIndex.value = Math.round(ratio * (total - 1))
}

function onPointerLeave(): void {
  hoverIndex.value = null
}

const hoverPoint = computed(() => {
  const index = hoverIndex.value
  if (index === null || index >= values.value.length) return null
  const value = values.value[index]
  if (value === undefined) return null
  return { index, x: xAt(index), y: yAt(value) }
})

// tooltip 横向位置用百分比定位到 SVG 上方，边缘处平移避免裁切。
const hoverStyle = computed(() => {
  const point = hoverPoint.value
  if (!point) return null
  const percent = (point.x / viewBoxWidth) * 100
  const flip = percent > 78 ? '-translate-x-full' : (percent < 22 ? 'translate-x-0' : '-translate-x-1/2')
  return { left: `${percent}%`, class: flip }
})

// Y-axis labels at the three gridline rows (0, midpoint, max). The midpoint is
// snapped to a "good number" (1/2/2.5/5 × 10^n) so absolute values stay
// readable on the chart (design-aesthetics P0#126): a raw max/2 can render as
// 33.5, which is a measurement, not a label.
const yAxisLabels = computed(() => [
  { y: baselineY, value: 0 },
  { y: (baselineY + topY) / 2, value: niceMidpoint(maxValue.value) },
  { y: topY, value: maxValue.value },
])

function niceMidpoint(maxValue: number): number {
  const raw = maxValue / 2
  const magnitude = 10 ** Math.floor(Math.log10(Math.max(raw, 1)))
  for (const base of [1, 2, 2.5, 5]) {
    const step = base * magnitude
    if (step >= raw) {
      return step
    }
  }
  return 10 * magnitude
}

function formatValue(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value)
}
</script>

<template>
  <section
    class="card overflow-hidden"
    :aria-label="title"
  >
    <div class="flex items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <div>
        <h3 class="text-sm font-semibold text-[var(--color-text)]">
          {{ title }}
        </h3>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          {{ metricLabel }}{{ rangeLabel ? ` · ${rangeLabel}` : '' }} · 来源：请求元数据
        </p>
      </div>
      <span class="badge-info">
        {{ series.length ? `${series.length} 个时间点` : '无数据' }}
      </span>
    </div>

    <div
      v-if="series.length === 0"
      class="flex min-h-48 items-center justify-center p-6 text-sm text-[var(--color-text-muted)]"
    >
      暂无趋势数据
    </div>

    <template v-else>
      <div class="relative p-4">
        <svg
          class="h-48 w-full touch-none"
          viewBox="0 0 720 220"
          role="img"
          :aria-label="`${title}，${metricLabel}`"
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
              <stop
                offset="0%"
                :stop-color="seriesColor"
                stop-opacity="0.28"
              />
              <stop
                offset="100%"
                :stop-color="seriesColor"
                stop-opacity="0.02"
              />
            </linearGradient>
          </defs>
          <line
            :x1="plotLeft"
            :y1="baselineY"
            :x2="720"
            :y2="baselineY"
            stroke="var(--color-border-strong)"
            stroke-width="1"
          />
          <line
            :x1="plotLeft"
            :y1="(baselineY + topY) / 2"
            :x2="720"
            :y2="(baselineY + topY) / 2"
            stroke="var(--color-border)"
            stroke-width="1"
            stroke-dasharray="4 6"
          />
          <line
            :x1="plotLeft"
            :y1="topY"
            :x2="720"
            :y2="topY"
            stroke="var(--color-border)"
            stroke-width="1"
            stroke-dasharray="4 6"
          />
          <text
            v-for="label in yAxisLabels"
            :key="label.y"
            :x="plotLeft - 8"
            :y="label.y + 4"
            class="fill-[var(--color-text-muted)]"
            font-size="11"
            text-anchor="end"
            font-family="ui-monospace, monospace"
          >
            {{ formatValue(label.value) }}
          </text>
          <path
            :d="areaPath"
            :fill="`url(#${gradientId})`"
          />
          <path
            :d="smoothPath"
            fill="none"
            :stroke="seriesColor"
            :stroke-dasharray="lineDash"
            stroke-width="2.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <!-- 十字线与吸附点：仅 hover 时渲染 -->
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
              r="5.5"
              fill="var(--color-elevated)"
              :stroke="seriesColor"
              stroke-width="2.5"
            />
          </g>
        </svg>

        <!-- HTML tooltip：跟随十字线，展示时间点与数值 -->
        <div
          v-if="hoverPoint && hoverStyle"
          class="pointer-events-none absolute top-6 z-10 min-w-32 rounded-lg border border-[var(--color-border)] bg-[var(--color-elevated)] px-3 py-2 shadow-[var(--shadow-md)]"
          :class="hoverStyle.class"
          :style="{ left: hoverStyle.left }"
          role="status"
        >
          <p class="font-mono-data text-[11px] text-[var(--color-text-muted)]">
            {{ series[hoverPoint.index]?.bucket }}
          </p>
          <p
            class="mt-0.5 font-mono-data text-sm font-semibold"
            :style="{ color: seriesColor }"
          >
            {{ formatValue(values[hoverPoint.index] ?? 0) }}
          </p>
        </div>
      </div>

      <details class="border-t border-[var(--color-border)] px-4 py-3 text-xs">
        <summary class="cursor-pointer rounded text-[var(--color-text-secondary)]">
          查看数据表
        </summary>
        <div
          class="mt-3 overflow-x-auto"
          tabindex="0"
          role="region"
          aria-label="趋势数据表，可横向滚动"
        >
          <table class="data-table">
            <thead>
              <tr>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  时间
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  {{ metricLabel }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(point, index) in series"
                :key="point.bucket"
              >
                <td class="data-table-td font-mono-data">
                  {{ point.bucket }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ formatValue(values[index] ?? 0) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </template>
  </section>
</template>
