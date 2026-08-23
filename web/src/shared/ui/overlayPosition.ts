export type HorizontalPlacement = 'start' | 'center' | 'end'

export function horizontalPlacement(position: number, width: number): HorizontalPlacement {
  if (width <= 0) return 'center'
  const ratio = position / width
  if (ratio <= 0.25) return 'start'
  if (ratio >= 0.75) return 'end'
  return 'center'
}
