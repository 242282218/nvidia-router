/** Shared value formatters. One implementation per format; pages must not
 * re-declare these inline.
 *
 * All date/time outputs are pinned to Asia/Shanghai (UTC+8) so that operators
 * see consistent timestamps regardless of their browser locale or deployment
 * server timezone.
 */

export interface DateFormatOptions {
  /** Append seconds to the "YYYY/MM/DD HH:mm" output. */
  seconds?: boolean
}

function parseDateInput(value: string | number | Date): Date | null {
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : value
  }
  if (typeof value === 'number') {
    const d = new Date(value)
    return Number.isNaN(d.getTime()) ? null : d
  }
  const str = String(value).trim()
  if (!str) return null

  // If SQLite timestamp without T, e.g. "2026-08-16 12:00:00"
  let normalized = str
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/.test(normalized)) {
    normalized = normalized.replace(' ', 'T')
  }
  // If no timezone offset or Z, treat as UTC
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?$/.test(normalized)) {
    normalized += 'Z'
  }

  const parsed = new Date(normalized)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

const shanghaiDateMinuteFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

const shanghaiDateSecondFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

const shanghaiTimeOnlyFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

function formatParts(date: Date, formatter: Intl.DateTimeFormat): {
  year?: string
  month?: string
  day?: string
  hour?: string
  minute?: string
  second?: string
} {
  const parts = formatter.formatToParts(date)
  const map: Record<string, string> = {}
  for (const part of parts) {
    if (part.type !== 'literal') {
      map[part.type] = part.value
    }
  }
  return map
}

/** Shanghai time "YYYY/MM/DD HH:mm(:ss)"; missing → "—", unparsable → the raw input. */
export function formatDate(value?: string | number | Date, options?: DateFormatOptions): string {
  if (value === undefined || value === null || value === '') return '—'
  const date = parseDateInput(value)
  if (!date) return typeof value === 'string' ? value : '—'
  const p = formatParts(date, options?.seconds ? shanghaiDateSecondFormatter : shanghaiDateMinuteFormatter)
  if (p.year && p.month && p.day && p.hour && p.minute) {
    const base = `${p.year}/${p.month}/${p.day} ${p.hour}:${p.minute}`
    return options?.seconds && p.second !== undefined ? `${base}:${p.second}` : base
  }
  return (options?.seconds ? shanghaiDateSecondFormatter : shanghaiDateMinuteFormatter).format(date)
}

/** Shanghai time "HH:mm:ss" clock for live feeds; missing/unparsable → "—". */
export function formatClock(value?: string | number | Date): string {
  if (value === undefined || value === null || value === '') return '—'
  const date = parseDateInput(value)
  if (!date) return '—'
  const p = formatParts(date, shanghaiTimeOnlyFormatter)
  if (p.hour && p.minute && p.second) {
    return `${p.hour}:${p.minute}:${p.second}`
  }
  return shanghaiTimeOnlyFormatter.format(date)
}

/** Shanghai time "HH:mm:ss" for a Date, e.g. freshness stamps ("更新于 18:03:22"). */
export function formatTimeOfDay(date: Date): string {
  if (!date || Number.isNaN(date.getTime())) return '—'
  const p = formatParts(date, shanghaiTimeOnlyFormatter)
  if (p.hour && p.minute && p.second) {
    return `${p.hour}:${p.minute}:${p.second}`
  }
  return shanghaiTimeOnlyFormatter.format(date)
}

/** Shanghai time "YYYY/MM/DD HH:mm:ss" for freshness stamps; missing → "—". */
export function formatLocalDateTime(value?: string | number | Date): string {
  if (value === undefined || value === null || value === '') return '—'
  const date = parseDateInput(value)
  if (!date) return '—'
  const p = formatParts(date, shanghaiDateSecondFormatter)
  if (p.year && p.month && p.day && p.hour && p.minute && p.second) {
    return `${p.year}/${p.month}/${p.day} ${p.hour}:${p.minute}:${p.second}`
  }
  return shanghaiDateSecondFormatter.format(date)
}

/** Latency with unit; undefined → "—" (a missing measurement is not zero). */
export function formatLatency(value: number | undefined): string {
  return value === undefined ? '—' : `${value} ms`
}
