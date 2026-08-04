<script setup lang="ts">
import { computed } from 'vue'

import type { MonitoringSeriesPoint } from './types'

type TrendMetric = 'requests' | 'failures' | 'latency' | 'tokens'

const props = defineProps<{
  series: MonitoringSeriesPoint[]
  metric: TrendMetric
  title: string
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

const points = computed(() => values.value.map((value, index) => {
  const x = values.value.length <= 1 ? 360 : (index / (values.value.length - 1)) * 720
  const y = 188 - (value / maxValue.value) * 160
  return `${x.toFixed(1)},${y.toFixed(1)}`
}).join(' '))

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
          {{ metricLabel }}
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
            x1="0"
            y1="188"
            x2="720"
            y2="188"
            stroke="var(--color-border-strong)"
            stroke-width="1"
          />
          <line
            x1="0"
            y1="108"
            x2="720"
            y2="108"
            stroke="var(--color-border)"
            stroke-width="1"
            stroke-dasharray="4 6"
          />
          <line
            x1="0"
            y1="28"
            x2="720"
            y2="28"
            stroke="var(--color-border)"
            stroke-width="1"
            stroke-dasharray="4 6"
          />
          <polyline
            :points="points"
            fill="none"
            stroke="var(--color-info)"
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
                <th class="data-table-th">
                  时间
                </th>
                <th class="data-table-th">
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
