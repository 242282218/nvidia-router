<script setup lang="ts">
import { computed } from 'vue'

import { smoothPath, formatChartValue, niceMidpoint, type Point } from '../../shared/charts/geometry'
import { formatBucketLabel, formatTokens } from './format'
import type { MonitoringSeriesPoint } from './types'

// 流量趋势（设计 §5.3）：Token 柱（成功色低饱和）+ 请求折线（info 靛蓝）双 Y 轴。
// 语义色沿用旧趋势图约定：requests=info、tokens=success，只换布局不换语义。
// 颜色不是唯一信息载体：每个柱/点带 <title> 文字值，底部保留数据表兜底。
defineOptions({ name: 'TrafficChart' })

const props = defineProps<{
  series: MonitoringSeriesPoint[]
  rangeLabel?: string
}>()

const VIEW_WIDTH = 720
const VIEW_HEIGHT = 232
const PAD = { top: 10, right: 46, bottom: 26, left: 46 }

const plotWidth = VIEW_WIDTH - PAD.left - PAD.right
const plotHeight = VIEW_HEIGHT - PAD.top - PAD.bottom

interface TrafficPoint {
  label: string
  calls: number
  tokens: number
  success: number
  failure: number
}

const points = computed<TrafficPoint[]>(() => props.series.map((point) => ({
  label: point.bucket,
  calls: point.request_count,
  tokens: point.prompt_tokens + point.completion_tokens,
  success: point.success_count,
  failure: point.failure_count,
})))

// 双 Y 轴各自取"好数字"上限：左轴 Token、右轴调用次数，中点刻度可读。
const tokenMax = computed(() => {
  const max = Math.max(...points.value.map((p) => p.tokens), 0)
  return max > 0 ? niceMidpoint(max) * 2 : 10
})

const callsMax = computed(() => {
  const max = Math.max(...points.value.map((p) => p.calls), 0)
  return max > 0 ? niceMidpoint(max) * 2 : 10
})

function x(index: number): number {
  const count = points.value.length
  if (count <= 1) return PAD.left + plotWidth / 2
  return PAD.left + (index / (count - 1)) * plotWidth
}

function tokenY(value: number): number {
  return PAD.top + plotHeight - (value / tokenMax.value) * plotHeight
}

function callsY(value: number): number {
  return PAD.top + plotHeight - (value / callsMax.value) * plotHeight
}

const barWidth = computed(() => {
  const count = points.value.length
  if (count === 0) return 0
  return Math.max(6, Math.min(26, (plotWidth / count) * 0.55))
})

const gridLines = [0.25, 0.5, 0.75, 1]

const linePath = computed(() => {
  const path = smoothPath(points.value.map((point, index) => ({ x: x(index), y: callsY(point.calls) } as Point)))
  return path
})

const linePoints = computed(() => points.value.map((point, index) => ({ cx: x(index), cy: callsY(point.calls) })))

// X 轴稀疏标注：24h 每 4 个标一个，7d/30d 每天标但 30d 每 5 个标一个。
const xLabelStep = computed(() => {
  const count = points.value.length
  if (count <= 8) return 1
  if (count <= 30) return 5
  return 4
})

function showXLabel(index: number): boolean {
  return index % xLabelStep.value === 0
}

function cellTitle(point: TrafficPoint): string {
  return `${point.label}：请求 ${formatChartValue(point.calls)}（成功 ${formatChartValue(point.success)} · 失败 ${formatChartValue(point.failure)}），Token ${formatTokens(point.tokens)}`
}
</script>

<template>
  <section
    class="card overflow-visible"
    aria-label="流量趋势"
    data-testid="traffic-chart"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <div>
        <h3 class="type-heading">
          流量趋势
        </h3>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          Token 与调用次数{{ rangeLabel ? ` · ${rangeLabel}` : '' }} · 来源：请求元数据
        </p>
      </div>
      <div
        class="flex items-center gap-3 text-xs text-[var(--color-text-muted)]"
        aria-hidden="true"
      >
        <span class="flex items-center gap-1.5">
          <span class="h-2 w-2 rounded-full bg-[color-mix(in_srgb,var(--color-info)_60%,var(--color-surface))]" />
          调用
        </span>
        <span class="flex items-center gap-1.5">
          <span class="h-2 w-2 rounded-[2px] bg-[color-mix(in_srgb,var(--color-success)_35%,var(--color-surface))]" />
          Token
        </span>
      </div>
    </div>

    <div
      v-if="points.length === 0"
      class="flex min-h-48 items-center justify-center p-6 text-sm text-[var(--color-text-muted)]"
    >
      暂无趋势数据
    </div>

    <template v-else>
      <div class="p-4">
        <svg
          :viewBox="`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`"
          class="w-full"
          role="img"
          aria-label="流量趋势组合图：柱为 Token 用量，折线为调用次数"
        >
          <!-- 横向浅虚线网格（CPA §27：只保留主横向网格） -->
          <g>
            <line
              v-for="ratio in gridLines"
              :key="ratio"
              :x1="PAD.left"
              :x2="VIEW_WIDTH - PAD.right"
              :y1="PAD.top + plotHeight * (1 - ratio)"
              :y2="PAD.top + plotHeight * (1 - ratio)"
              stroke="var(--color-border-subtle)"
              stroke-dasharray="3 3"
            />
          </g>
          <!-- 基线 -->
          <line
            :x1="PAD.left"
            :x2="VIEW_WIDTH - PAD.right"
            :y1="PAD.top + plotHeight"
            :y2="PAD.top + plotHeight"
            stroke="var(--color-border)"
          />

          <!-- Token 柱（左轴） -->
          <g
            v-for="(point, index) in points"
            :key="`bar-${point.label}`"
          >
            <rect
              :x="x(index) - barWidth / 2"
              :y="tokenY(point.tokens)"
              :width="barWidth"
              :height="Math.max(0, PAD.top + plotHeight - tokenY(point.tokens))"
              rx="2.5"
              fill="color-mix(in srgb, var(--color-success) 35%, var(--color-surface))"
              stroke="color-mix(in srgb, var(--color-success) 55%, var(--color-surface))"
              stroke-width="0.5"
            >
              <title>{{ cellTitle(point) }}</title>
            </rect>
          </g>

          <!-- 调用折线（右轴） -->
          <path
            :d="linePath"
            fill="none"
            stroke="var(--color-info)"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <g
            v-for="(point, index) in points"
            :key="`dot-${point.label}`"
          >
            <circle
              :cx="linePoints[index]?.cx"
              :cy="linePoints[index]?.cy"
              r="2.6"
              fill="var(--color-surface)"
              stroke="var(--color-info)"
              stroke-width="1.5"
            >
              <title>{{ cellTitle(point) }}</title>
            </circle>
          </g>

          <!-- 轴刻度：左 Token / 右调用 -->
          <g class="font-mono-data">
            <text
              v-for="ratio in gridLines"
              :key="`ly-${ratio}`"
              :x="PAD.left - 6"
              :y="PAD.top + plotHeight * (1 - ratio) + 3"
              text-anchor="end"
              font-size="9"
              fill="var(--color-text-subtle)"
            >{{ formatTokens(tokenMax * ratio) }}</text>
            <text
              v-for="ratio in gridLines"
              :key="`ry-${ratio}`"
              :x="VIEW_WIDTH - PAD.right + 6"
              :y="PAD.top + plotHeight * (1 - ratio) + 3"
              text-anchor="start"
              font-size="9"
              fill="var(--color-text-subtle)"
            >{{ formatChartValue(callsMax * ratio) }}</text>
          </g>

          <!-- X 轴稀疏标签 -->
          <g class="font-mono-data">
            <text
              v-for="(point, index) in points"
              v-show="showXLabel(index)"
              :key="`x-${point.label}`"
              :x="x(index)"
              :y="VIEW_HEIGHT - 8"
              text-anchor="middle"
              font-size="9"
              fill="var(--color-text-subtle)"
            >{{ formatBucketLabel(point.label) }}</text>
          </g>
        </svg>
      </div>

      <details class="border-t border-[var(--color-border)] px-4 py-3 text-xs">
        <summary class="cursor-pointer rounded text-[var(--color-text-secondary)]">
          查看数据表
        </summary>
        <div
          class="mt-3 overflow-x-auto"
          tabindex="0"
          role="region"
          aria-label="流量趋势数据表，可横向滚动"
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
                  调用
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  成功
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  失败
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  Token
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
                  {{ formatChartValue(point.calls) }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ formatChartValue(point.success) }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ formatChartValue(point.failure) }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ formatTokens(point.tokens) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </template>
  </section>
</template>
