// 图表几何工具：平滑曲线与坐标轴刻度的纯函数集合。
// 全部为无副作用函数，便于单测快照。

export interface Point {
  x: number
  y: number
}

/**
 * Catmull-Rom → cubic Bézier：把折线转成平滑曲线，张力 0.5 抑制过冲，
 * 数值不会画出 [minY, maxY] 之外（监控图不允许曲线"编造"不存在的峰值）。
 */
export function smoothPath(points: readonly Point[]): string {
  if (points.length === 0) return ''
  const first = points[0]
  if (!first) return ''
  if (points.length === 1) return `M${first.x.toFixed(1)},${first.y.toFixed(1)}`
  let path = `M${first.x.toFixed(1)},${first.y.toFixed(1)}`
  for (let i = 0; i < points.length - 1; i++) {
    const p1 = points[i]
    const p2 = points[i + 1]
    const p0 = points[Math.max(0, i - 1)]
    const p3 = points[Math.min(points.length - 1, i + 2)]
    if (!p1 || !p2 || !p0 || !p3) break
    const c1x = p1.x + ((p2.x - p0.x) / 6)
    const c1y = p1.y + ((p2.y - p0.y) / 6)
    const c2x = p2.x - ((p3.x - p1.x) / 6)
    const c2y = p2.y - ((p3.y - p1.y) / 6)
    path += ` C${c1x.toFixed(1)},${c1y.toFixed(1)} ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2.x.toFixed(1)},${p2.y.toFixed(1)}`
  }
  return path
}

/**
 * Y 轴中点取"好数字"（1/2/2.5/5 × 10^n），让绝对值在图上可读：
 * 原始 max/2 可能渲染出 33.5 这种测量值而非标签。
 */
export function niceMidpoint(maxValue: number): number {
  const raw = maxValue / 2
  const magnitude = 10 ** Math.floor(Math.log10(Math.max(raw, 1)))
  for (const base of [1, 2, 2.5, 5]) {
    const step = base * magnitude
    if (step >= raw) return step
  }
  return 10 * magnitude
}

/** zh-CN 千分位、最多 1 位小数——图表数值的统一格式。 */
export function formatChartValue(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value)
}
