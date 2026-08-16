import { describe, expect, it } from 'vitest'

import { formatClock, formatDate, formatLatency, formatLocalDateTime } from './format'

describe('formatDate', () => {
  it('formats a UTC timestamp without seconds by default', () => {
    expect(formatDate('2026-08-16T09:05:07Z')).toBe('2026/08/16 09:05')
  })

  it('appends seconds when requested', () => {
    expect(formatDate('2026-08-16T09:05:07Z', { seconds: true })).toBe('2026/08/16 09:05:07')
  })

  it('returns an em dash for missing values instead of pretending zero', () => {
    expect(formatDate()).toBe('—')
    expect(formatDate('')).toBe('—')
  })

  it('returns the raw input when it cannot be parsed', () => {
    expect(formatDate('not-a-date')).toBe('not-a-date')
  })
})

describe('formatClock', () => {
  it('formats the local clock with seconds', () => {
    const input = '2026-08-16T09:05:07Z'
    const expected = new Date(input)
    const pad = (n: number) => String(n).padStart(2, '0')
    expect(formatClock(input)).toBe(`${pad(expected.getHours())}:${pad(expected.getMinutes())}:${pad(expected.getSeconds())}`)
  })

  it('returns an em dash for missing or unparsable values', () => {
    expect(formatClock()).toBe('—')
    expect(formatClock('bad')).toBe('—')
  })
})

describe('formatLocalDateTime', () => {
  it('formats local date and time', () => {
    const input = '2026-08-16T09:05:07Z'
    const date = new Date(input)
    const pad = (n: number) => String(n).padStart(2, '0')
    const expected = `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
    expect(formatLocalDateTime(input)).toBe(expected)
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
