import { describe, expect, it } from 'vitest'

import { formatClock, formatDate, formatLatency, formatLocalDateTime, formatTimeOfDay } from './format'

describe('formatDate', () => {
  it('formats a timestamp in Shanghai timezone (UTC+8) without seconds by default', () => {
    expect(formatDate('2026-08-16T09:05:07Z')).toBe('2026/08/16 17:05')
  })

  it('appends seconds when requested in Shanghai timezone', () => {
    expect(formatDate('2026-08-16T09:05:07Z', { seconds: true })).toBe('2026/08/16 17:05:07')
  })

  it('handles SQLite UTC timestamps with space separator', () => {
    expect(formatDate('2026-08-16 09:05:07', { seconds: true })).toBe('2026/08/16 17:05:07')
  })

  it('crosses midnight into the next day when UTC+8 wraps', () => {
    expect(formatDate('2026-08-16T20:00:00Z')).toBe('2026/08/17 04:00')
  })

  it('formats Date object in Shanghai timezone', () => {
    const d = new Date('2026-08-16T09:05:07Z')
    expect(formatDate(d)).toBe('2026/08/16 17:05')
  })

  it('returns an em dash for missing values instead of pretending zero', () => {
    expect(formatDate()).toBe('—')
    expect(formatDate('')).toBe('—')
    expect(formatDate(undefined)).toBe('—')
  })

  it('returns the raw input when it cannot be parsed', () => {
    expect(formatDate('not-a-date')).toBe('not-a-date')
  })
})

describe('formatClock', () => {
  it('formats the Shanghai clock with seconds', () => {
    const input = '2026-08-16T09:05:07Z'
    expect(formatClock(input)).toBe('17:05:07')
  })

  it('formats SQLite timestamp in Shanghai clock', () => {
    expect(formatClock('2026-08-16 09:05:07')).toBe('17:05:07')
  })

  it('returns an em dash for missing or unparsable values', () => {
    expect(formatClock()).toBe('—')
    expect(formatClock('bad')).toBe('—')
  })
})

describe('formatTimeOfDay', () => {
  it('formats Date object in Shanghai HH:mm:ss', () => {
    const d = new Date('2026-08-16T09:05:07Z')
    expect(formatTimeOfDay(d)).toBe('17:05:07')
  })
})

describe('formatLocalDateTime', () => {
  it('formats date and time in Shanghai timezone', () => {
    const input = '2026-08-16T09:05:07Z'
    expect(formatLocalDateTime(input)).toBe('2026/08/16 17:05:07')
  })

  it('formats SQLite timestamp in Shanghai timezone', () => {
    expect(formatLocalDateTime('2026-08-16 09:05:07')).toBe('2026/08/16 17:05:07')
  })

  it('returns an em dash for missing or unparsable values', () => {
    expect(formatLocalDateTime()).toBe('—')
    expect(formatLocalDateTime('bad')).toBe('—')
  })
})

describe('formatLatency', () => {
  it('appends the unit to a measurement', () => {
    expect(formatLatency(120)).toBe('120 ms')
  })

  it('keeps undefined distinct from zero', () => {
    expect(formatLatency(undefined)).toBe('—')
    expect(formatLatency(0)).toBe('0 ms')
  })
})
