<script setup lang="ts">
import { computed } from 'vue'

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

const points = computed(() => values.value.map((value, index) => {
  const x = values.value.length <= 1 ? plotLeft + plotWidth / 2 : plotLeft + (index / (values.value.length - 1)) * plotWidth
  const y = 188 - (value / maxValue.value) * 160
  return `${x.toFixed(1)},${y.toFixed(1)}`
}).join(' '))

// Plot geometry: a 48px left gutter carries the Y-axis labels so the line chart
// stays readable without shifting the SVG viewBox.
const plotLeft = 48
const plotWidth = 720 - plotLeft

// Y-axis labels at the three gridline rows (0, midpoint, max). The midpoint is
// snapped to a "good number" (1/2/2.5/5 × 10^n) so absolute values stay
// readable on the chart (design-aesthetics P0#126): a raw max/2 can render as
// 33.5, which is a measurement, not a label.
const yAxisLabels = computed(() => [
  { y: 188, value: 0 },
  { y: 108, value: niceMidpoint(maxValue.value) },
  { y: 28, value: maxValue.value },
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
      <div class="p-4">
        <svg
          class="h-48 w-full"
          viewBox="0 0 720 220"
          role="img"
          :aria-label="`${title}，${metricLabel}`"
        >
          <line
            :x1="plotLeft"
            y1="188"
            :x2="720"
            y2="188"
            stroke="var(--color-border-strong)"
            stroke-width="1"
          />
          <line
            :x1="plotLeft"
            y1="108"
            :x2="720"
            y2="108"
            stroke="var(--color-border)"
            stroke-width="1"
            stroke-dasharray="4 6"
          />
          <line
            :x1="plotLeft"
            y1="28"
            :x2="720"
            y2="28"
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
          <polyline
            :points="points"
            fill="none"
            :stroke="seriesColor"
            :stroke-dasharray="lineDash"
            stroke-width="3"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </div>

      <details class="border-t border-[var(--color-border)] px-4 py-3 text-xs">
        <summary class="cursor-pointer text-[var(--color-text-secondary)]">
          查看数据表
        </summary>
        <div class="mt-3 overflow-x-auto">
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
                <td class="data-table-td font-mono">
                  {{ point.bucket }}
                </td>
                <td class="data-table-td font-mono">
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
