import { describe, expect, it } from 'vitest'

import { horizontalPlacement } from './overlayPosition'

describe('horizontalPlacement', () => {
  it('aligns near the left edge without translating past the host', () => {
    expect(horizontalPlacement(8, 200)).toBe('start')
  })

  it('centers points away from both edges', () => {
    expect(horizontalPlacement(100, 200)).toBe('center')
  })

  it('aligns near the right edge without translating past the host', () => {
    expect(horizontalPlacement(192, 200)).toBe('end')
  })

  it('uses center for an empty host', () => {
    expect(horizontalPlacement(0, 0)).toBe('center')
  })
})
