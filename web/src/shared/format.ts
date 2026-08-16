/** Shared value formatters. One implementation per format; pages must not
 * re-declare these inline (the pre-refactor codebase had six diverging
 * copies of formatDate alone). */

export interface DateFormatOptions {
  /** Append seconds to the "YYYY/MM/DD HH:mm" output. */
  seconds?: boolean
}

function pad(part: number): string {
  return String(part).padStart(2, '0')
}

function parseDate(value: string): Date | null {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/** UTC "YYYY/MM/DD HH:mm(:ss)"; missing → "—", unparsable → the raw input. */
export function formatDate(value?: string, options?: DateFormatOptions): string {
  if (!value) return '—'
  const date = parseDate(value)
  if (!date) return value
  const base = `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
  return options?.seconds ? `${base}:${pad(date.getUTCSeconds())}` : base
}

/** Local "HH:mm:ss" clock for live feeds; missing/unparsable → "—". */
export function formatClock(value?: string): string {
  if (!value) return '—'
  const date = parseDate(value)
  if (!date) return '—'
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

/** Local "YYYY/MM/DD HH:mm:ss" for freshness stamps; missing → "—". */
export function formatLocalDateTime(value?: string): string {
  if (!value) return '—'
  const date = parseDate(value)
  if (!date) return '—'
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

/** Latency with unit; undefined → "—" (a missing measurement is not zero). */
export function formatLatency(value: number | undefined): string {
  return value === undefined ? '—' : `${value} ms`
}
