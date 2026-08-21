<script setup lang="ts">
import { computed } from 'vue'

// 热力图：日×模型成本矩阵、日×状态密度等网格数据。
// 无障碍红线（memory 2026-08-20）：每个格子必须同时提供文字 title，
// 不能只依赖颜色深浅传达数值。
defineOptions({ name: 'ChartHeatmap' })

const props = withDefaults(defineProps<{
  rowLabels: readonly string[]
  colLabels: readonly string[]
  /** values[rowIndex][colIndex]，缺省（null）表示无数据。 */
  values: readonly (readonly (number | null)[])[]
  /** 色标基色（CSS 变量），透明度按数值比例映射。 */
  color: string
  /** 数值格式化（如成本带 $ 前缀）。 */
  format?: (value: number) => string
  ariaLabel: string
}>(), {
  format: (value: number) => new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value),
})

const maxValue = computed(() => {
  let max = 0
  for (const row of props.values) {
    for (const cell of row) {
      if (cell !== null && cell > max) max = cell
    }
  }
  return max
})

function cellStyle(value: number | null): Record<string, string> {
  if (value === null || maxValue.value === 0) return { background: 'var(--color-sunken)' }
  const intensity = 0.08 + (value / maxValue.value) * 0.82
  return {
    background: `color-mix(in srgb, ${props.color} ${Math.round(intensity * 100)}%, var(--color-surface))`,
  }
}

function cellTitle(rowLabel: string, colLabel: string, value: number | null): string {
  return value === null
    ? `${rowLabel} × ${colLabel}：无数据`
    : `${rowLabel} × ${colLabel}：${props.format(value)}`
}
</script>

<template>
  <div
    class="overflow-x-auto"
    tabindex="0"
    role="region"
    :aria-label="ariaLabel"
  >
    <table class="border-separate border-spacing-[3px]">
      <thead>
        <tr>
          <th scope="col" />
          <th
            v-for="col in colLabels"
            :key="col"
            class="font-mono-data px-1 pb-1 text-[10px] font-medium text-[var(--color-text-subtle)]"
            scope="col"
          >
            {{ col }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(row, ri) in rowLabels"
          :key="row"
        >
          <th class="pr-2 text-right text-xs font-medium text-[var(--color-text-muted)]">
            {{ row }}
          </th>
          <td
            v-for="(_, ci) in colLabels"
            :key="ci"
            class="h-7 min-w-7 rounded-[5px] transition-transform duration-100 hover:scale-110"
            :style="cellStyle(values[ri]?.[ci] ?? null)"
            :title="cellTitle(row, colLabels[ci] ?? '', values[ri]?.[ci] ?? null)"
          />
        </tr>
      </tbody>
    </table>
  </div>
</template>
