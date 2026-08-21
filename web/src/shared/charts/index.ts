// 图表套件统一出口：全站可视化从 shared/charts 一处取用。
// 约定：语义色由调用方以 CSS 变量传入；每条序列必须有非颜色的第二编码
// （虚线/图例/文字），reduced-motion 下动画自动退化。
export { default as ChartArea } from './ChartArea.vue'
export { default as ChartBars, type BarSeries } from './ChartBars.vue'
export { default as ChartDonut, type DonutSegment } from './ChartDonut.vue'
export { default as ChartHeatmap } from './ChartHeatmap.vue'
export { default as ChartSparkline } from './ChartSparkline.vue'
export { formatChartValue, niceMidpoint, smoothPath } from './geometry'
