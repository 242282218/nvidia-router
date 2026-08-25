<script setup lang="ts">
import { computed } from 'vue'

import { formatAverageLatency, formatBucketLabel } from './format'
import type { MonitoringRange, MonitoringSeriesPoint } from './types'

// 请求健康时间线（设计 §5.3）：时间格一行，状态色 3 档透明度承载密度，
// 数值全部走 title 文字（无障碍红线：颜色不是唯一载体）。桶粒度跟随 API：
// 24h→小时格、7d/30d→天格；滚动窗口内理论上没有未来格，判定保留作防御
// （服务器时钟偏移时仍能正确降级为空心格）。
defineOptions({ name: 'HealthTimeline' })

const props = defineProps<{
  series: MonitoringSeriesPoint[]
  range: MonitoringRange
  from?: string
  to?: string
}>()

type CellStatus = 'future' | 'empty' | 'success' | 'warning' | 'danger'

interface TimelineCell {
  label: string
  status: CellStatus
  intensity: number
  title: string
}

// 状态色 → 基色变量；intensity 为 color-mix 百分比（3 档，对齐 ChartHeatmap）。
const statusColor: Record<Exclude<CellStatus, 'future' | 'empty'>, string> = {
  success: 'var(--color-success)',
  warning: 'var(--color-warning)',
  danger: 'var(--color-danger)',
}

function tier(ratio: number): number {
  if (ratio < 0.34) return 0.16
  if (ratio < 0.67) return 0.42
  return 0.74
}

function parseDate(value: string | undefined): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? null : parsed
}

const cells = computed<TimelineCell[]>(() => {
  const count = props.series.length
  const fromMs = parseDate(props.from)
  const toMs = parseDate(props.to)
  const now = Date.now()
  // 密度基准：窗口内最大请求量，决定成功格的相对深浅。
  const maxRequests = Math.max(...props.series.map((point) => point.request_count), 0)

  return props.series.map((point, index) => {
    const bucketStart
      = fromMs !== null && toMs !== null && count > 0
        ? fromMs + ((toMs - fromMs) * index) / count
        : null
    const future = bucketStart !== null && bucketStart > now

    let status: CellStatus = 'empty'
    let intensity = 0
    if (future) {
      status = 'future'
    } else if (point.request_count > 0) {
      if (point.failure_count > 0) {
        status = 'danger'
        intensity = tier(point.failure_count / point.request_count)
      } else if (point.canceled_count > 0) {
        status = 'warning'
        intensity = tier(point.canceled_count / point.request_count)
      } else {
        status = 'success'
        intensity = maxRequests > 0 ? tier(point.request_count / maxRequests) : 0.16
      }
    }

    const successRate = point.request_count > 0
      ? `${((point.success_count / point.request_count) * 100).toFixed(1)}%`
      : '—'

    return {
      label: point.bucket,
      status,
      intensity,
      title: future
        ? `${point.bucket}：未来时间`
        : `${point.bucket}：请求 ${point.request_count} · 成功 ${point.success_count} · 取消 ${point.canceled_count} · 失败 ${point.failure_count} · 成功率 ${successRate} · 平均延迟 ${formatAverageLatency(point.average_duration_ms)}`,
    }
  })
})

function cellStyle(cell: TimelineCell): Record<string, string> {
  if (cell.status === 'future') {
    return {
      background: 'var(--color-surface)',
      'border-color': 'var(--color-border-strong)',
      'border-style': 'dashed',
    }
  }
  if (cell.status === 'empty') return { background: 'var(--color-sunken)' }
  return {
    background: `color-mix(in srgb, ${statusColor[cell.status]} ${Math.round(cell.intensity * 100)}%, var(--color-surface))`,
  }
}

const labelStep = computed(() => {
  const count = cells.value.length
  if (count <= 8) return 1
  if (count <= 30) return 5
  return 4
})

const unitLabel = computed(() => (props.range === '24h' ? '按小时聚合' : '按天聚合'))

const legendItems = [
  { label: '无数据', style: { background: 'var(--color-sunken)' } },
  { label: '成功', style: { background: 'color-mix(in srgb, var(--color-success) 42%, var(--color-surface))' } },
  { label: '取消', style: { background: 'color-mix(in srgb, var(--color-warning) 42%, var(--color-surface))' } },
  { label: '失败', style: { background: 'color-mix(in srgb, var(--color-danger) 42%, var(--color-surface))' } },
  { label: '未来', style: { background: 'var(--color-surface)', borderColor: 'var(--color-border-strong)', borderStyle: 'dashed' } },
]
</script>

<template>
  <section
    class="card overflow-hidden"
    aria-label="请求健康时间线"
    data-testid="health-timeline"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <div>
        <h3 class="type-heading">
          健康时间线
        </h3>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          请求结果按时间格展示 · {{ unitLabel }} · 取消为客户端中断
        </p>
      </div>
    </div>

    <div
      v-if="cells.length === 0"
      class="flex min-h-48 items-center justify-center p-6 text-sm text-[var(--color-text-muted)]"
    >
      暂无时间线数据
    </div>

    <template v-else>
      <div class="p-4">
        <div
          class="overflow-x-auto"
          tabindex="0"
          role="region"
          aria-label="请求健康时间线，可横向滚动"
        >
          <div class="min-w-max">
            <ol
              class="grid gap-1"
              :style="{ gridTemplateColumns: `repeat(${cells.length}, minmax(22px, 1fr))` }"
              data-testid="health-timeline-cells"
            >
              <li
                v-for="cell in cells"
                :key="cell.label"
                class="h-7 min-w-[22px] rounded-[var(--radius-data)] border border-transparent transition-transform duration-100 hover:scale-110"
                :style="cellStyle(cell)"
                :title="cell.title"
              />
            </ol>
            <div
              class="mt-1.5 grid gap-1"
              :style="{ gridTemplateColumns: `repeat(${cells.length}, minmax(22px, 1fr))` }"
            >
              <span
                v-for="(cell, index) in cells"
                :key="`label-${cell.label}`"
                class="text-center font-mono-data text-xs text-[var(--color-text-subtle)]"
              >
                {{ index % labelStep === 0 ? formatBucketLabel(cell.label) : '' }}
              </span>
            </div>
          </div>
        </div>

        <div
          class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1"
          aria-hidden="true"
        >
          <span
            v-for="item in legendItems"
            :key="item.label"
            class="flex items-center gap-1 text-xs text-[var(--color-text-muted)]"
          >
            <span
              class="h-2.5 w-2.5 rounded-[var(--radius-data)] border border-transparent"
              :style="item.style"
            />
            {{ item.label }}
          </span>
        </div>
        <p class="sr-only">
          时间线各格数值请将鼠标悬停在格子上查看，或使用下方请求明细表。
        </p>
      </div>
    </template>
  </section>
</template>
