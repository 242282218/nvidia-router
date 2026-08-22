/** Statistics-page number formatting. Shared by the KPI grid, the trend
 * charts and the logs table so thousands separators, percentage precision
 * and compact tokens stay identical across the page (04-数据可视化 P1#6:
 * 千分位、小数位、精度整页统一). */
import { formatDate as formatUTCDate } from '../../shared/format'

export function formatInteger(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(value)
}

export function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

/** Averages keep one decimal; a bare count would hide sub-ms differences. */
export function formatAverageLatency(value: number | undefined): string {
  return value === undefined ? '—' : `${value.toFixed(1)} ms`
}

export function formatTokens(value: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

/** Request-log timestamps are UTC (server clocks); seconds matter when
 * correlating with upstream logs. */
export function formatLogDate(value: string): string {
  return formatUTCDate(value, { seconds: true })
}

/** Shorten monitoring bucket labels for chart axes: hourly
 * "2026-08-22T09:00:00Z" → "09:00", daily "2026-08-22" → "08-22".
 * Buckets are UTC; keep them unconverted so the axis matches the raw
 * series (same convention as the pre-split trend chart data table). */
export function formatBucketLabel(bucket: string): string {
  if (bucket.length === 10) return bucket.slice(5)
  const match = /T(\d{2}:\d{2})/.exec(bucket)
  return match?.[1] ?? bucket
}
