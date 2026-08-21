<script setup lang="ts">
import { computed } from 'vue'

import { ChartArea } from '../../shared/charts'
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

const points = computed(() => props.series.map((point) => ({
  label: point.bucket,
  value: metricValue(point),
})))

function metricValue(point: MonitoringSeriesPoint): number {
  if (props.metric === 'latency') return point.average_duration_ms
  if (props.metric === 'tokens') return point.prompt_tokens + point.completion_tokens
  if (props.metric === 'failures') return point.failure_count
  return point.request_count
}

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
        <ChartArea
          :points="points"
          :color="seriesColor"
          :dashed="metric === 'failures'"
          :ariaLabel="`${title}，${metricLabel}`"
        />
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
                v-for="point in points"
                :key="point.label"
              >
                <td class="data-table-td font-mono-data">
                  {{ point.label }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(point.value) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </template>
  </section>
</template>
