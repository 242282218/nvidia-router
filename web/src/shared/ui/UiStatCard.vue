<script setup lang="ts">
import { computed } from 'vue'

import { useCountUp } from '../useCountUp'
import UiIcon from './UiIcon.vue'
import type { IconName } from './icons'

// KPI 指标卡（docs/plans/2026-08-22-观测拆分与CPA布局复刻设计.md §4.1/§5.2）：
// 30px 语义浅 Tint 图标块 + 标题 + 大数字 + 辅助行。CPA 布局结构、Warm 皮肤。
// 与 UiStat 的分工：UiStat 是无图标的纯指标块（旧页面在用）；本组件用于
// KPI 平铺行，图标块承担快速语义扫描（tone 只落在图标块与数值局部，不整卡染色）。
defineOptions({ name: 'UiStatCard' })

const props = withDefaults(defineProps<{
  /** 指标名（12px 标签档）。 */
  label: string
  /** 主数值：字符串原样展示；数字走 count-up 与 format。 */
  value: string | number
  /** 辅助行（≤12px 底线上的次级信息，可省略）。 */
  hint?: string
  /** 语义色：控制图标块底色/前景与数值强调色。 */
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
  icon?: IconName
  /** 数值型 value 的格式化函数；缺省千分位整数。 */
  format?: (n: number) => string
  /** 数值是否用语义色强调（默认仅图标块着色，数值保持墨色）。 */
  toneValue?: boolean
}>(), { hint: undefined, tone: 'default', icon: undefined, format: undefined, toneValue: false })

const toneBlock: Record<string, string> = {
  default: 'bg-[var(--color-muted-background)] text-[var(--color-muted-foreground)]',
  success: 'bg-[var(--color-success-background)] text-[var(--color-success-foreground)]',
  warning: 'bg-[var(--color-warning-background)] text-[var(--color-warning-foreground)]',
  danger: 'bg-[var(--color-danger-background)] text-[var(--color-danger-foreground)]',
  info: 'bg-[var(--color-info-background)] text-[var(--color-info-foreground)]',
}

const toneValueClass: Record<string, string> = {
  default: 'text-[var(--color-text)]',
  success: 'text-[var(--color-success-text)]',
  warning: 'text-[var(--color-warning-text)]',
  danger: 'text-[var(--color-danger-text)]',
  info: 'text-[var(--color-info-text)]',
}

const integerFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 })

const numericTarget = computed(() => (typeof props.value === 'number' ? props.value : 0))
const animated = useCountUp(numericTarget)

const displayValue = computed(() => {
  if (typeof props.value === 'string') return props.value
  if (props.format) return props.format(animated.value)
  return integerFormat.format(Math.round(animated.value))
})
</script>

<template>
  <div class="metric-card flex flex-col gap-2.5 p-4">
    <div class="flex items-center gap-2.5">
      <span
        v-if="icon"
        class="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-[var(--radius-control)]"
        :class="toneBlock[tone]"
        aria-hidden="true"
      >
        <UiIcon
          :name="icon"
          :size="16"
        />
      </span>
      <p class="type-label flex-1 truncate">
        {{ label }}
      </p>
    </div>
    <p
      class="font-mono-data text-[22px] font-semibold leading-tight tracking-[var(--tracking-display)] tabular-nums"
      :class="toneValue ? toneValueClass[tone] : 'text-[var(--color-text)]'"
    >
      {{ displayValue }}
    </p>
    <p
      v-if="hint"
      class="text-xs leading-relaxed text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
    <!-- 无 hint 时占位，保证同一行 KPI 卡基线对齐（高度不随辅助行抖动）。 -->
    <p
      v-else
      class="text-xs leading-relaxed text-transparent"
      aria-hidden="true"
    >
      ·
    </p>
  </div>
</template>
