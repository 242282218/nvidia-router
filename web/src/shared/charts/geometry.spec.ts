import { describe, expect, it } from 'vitest'

import { formatChartValue, niceMidpoint, smoothPath } from './geometry'

describe('niceMidpoint', () => {
  it('snaps midpoint to good numbers', () => {
    expect(niceMidpoint(100)).toBe(50)
    expect(niceMidpoint(67)).toBe(50)
    expect(niceMidpoint(80)).toBe(50)
    expect(niceMidpoint(90)).toBe(50)
    expect(niceMidpoint(120)).toBe(100)
  })

  it('handles tiny and zero ranges', () => {
    expect(niceMidpoint(0)).toBe(1)
    expect(niceMidpoint(2)).toBe(1)
  })
})

describe('smoothPath', () => {
  it('returns empty for no points', () => {
    expect(smoothPath([])).toBe('')
  })

  it('returns a move for a single point', () => {
    expect(smoothPath([{ x: 10, y: 20 }])).toBe('M10.0,20.0')
  })

  it('builds cubic segments that stay monotonic in x', () => {
    const path = smoothPath([
      { x: 0, y: 100 },
      { x: 50, y: 40 },
      { x: 100, y: 70 },
    ])
    expect(path.startsWith('M0.0,100.0')).toBe(true)
    expect(path.match(/C/g)).toHaveLength(2)
  })
})

describe('formatChartValue', () => {
  it('formats with zh-CN grouping and up to 1 decimal', () => {
    expect(formatChartValue(12345)).toBe('12,345')
    expect(formatChartValue(3.14)).toBe('3.1')
  })
})
