<script setup lang="ts">
import { computed } from 'vue'

import { ChartSparkline } from '../charts'
import { useCountUp } from '../useCountUp'

// 指标块：label + 大数字 + 补充说明 + 可选迷你趋势。只读遥测，无 hover 反馈。
// tone 仅在指标自带语义时使用（成功率/失败数等），默认中性墨色。
defineOptions({ name: 'UiStat' })

const props = withDefaults(defineProps<{
  label: string
  value: string | number
  hint?: string
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
  /** 等宽数字字体，便于纵向对齐比较。 */
  mono?: boolean
  /** 迷你趋势序列（与指标同窗口）；提供时在卡片底部渲染 sparkline。 */
  sparkline?: number[]
  /** 数值型 value 的格式化函数；缺省千分位整数。 */
  format?: (n: number) => string
  /** Warm Restraint：主 KPI 放大档——display 字阶 + 收紧字距，一屏至多三四个。 */
  prominent?: boolean
}>(), { hint: undefined, tone: 'default', mono: true, sparkline: undefined, format: undefined, prominent: false })

const toneClass: Record<string, string> = {
  default: 'text-[var(--color-text)]',
  success: 'text-[var(--color-success)]',
  warning: 'text-[var(--color-warning)]',
  danger: 'text-[var(--color-danger)]',
  info: 'text-[var(--color-info)]',
}

const toneColorVar: Record<string, string> = {
  default: 'var(--color-info)',
  success: 'var(--color-success)',
  warning: 'var(--color-warning)',
  danger: 'var(--color-danger)',
  info: 'var(--color-info)',
}

const integerFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 })

// 数值型 value 走 count-up；字符串（预格式化）原样展示。
const numericTarget = computed(() => (typeof props.value === 'number' ? props.value : 0))
const animated = useCountUp(numericTarget)

const displayValue = computed(() => {
  if (typeof props.value === 'string') return props.value
  if (props.format) return props.format(animated.value)
  return integerFormat.format(Math.round(animated.value))
})
</script>

<template>
  <div class="metric-card flex flex-col">
    <p class="type-label">
      {{ label }}
    </p>
    <p
      class="mt-2 font-semibold leading-tight"
      :class="[
        toneClass[tone],
        prominent ? 'stat-display' : mono ? 'font-mono-data text-[22px]' : 'text-[22px]',
      ]"
    >
      {{ displayValue }}
    </p>
    <p
      v-if="hint"
      class="mt-1.5 text-xs text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
    <ChartSparkline
      v-if="sparkline && sparkline.length >= 2"
      class="mt-4"
      :values="sparkline"
      :color="toneColorVar[tone]"
    />
  </div>
</template>

<style scoped>
/* Warm Restraint 主 KPI 档：display 字阶（含 600 字重与 1.2 行高）+ 收紧字距。
   tabular-nums 保证 count-up 时数字宽度不抖动；覆盖 mono 的字体族。 */
.stat-display {
  font: var(--text-display);
  letter-spacing: var(--tracking-display);
  font-variant-numeric: tabular-nums;
}
</style>
