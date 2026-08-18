<script setup lang="ts">
// 指标块：label + 大数字 + 补充说明。只读遥测，无 hover 反馈（metric-card）。
// tone 仅在指标自带语义时使用（成功率/失败数等），默认中性墨色。
defineOptions({ name: 'UiStat' })

withDefaults(defineProps<{
  label: string
  value: string | number
  hint?: string
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
  /** 等宽数字字体，便于纵向对齐比较。 */
  mono?: boolean
}>(), { hint: undefined, tone: 'default', mono: true })

const toneClass: Record<string, string> = {
  default: 'text-[var(--color-text)]',
  success: 'text-[var(--color-success)]',
  warning: 'text-[var(--color-warning)]',
  danger: 'text-[var(--color-danger)]',
  info: 'text-[var(--color-info)]',
}
</script>

<template>
  <div class="metric-card">
    <p class="type-label">
      {{ label }}
    </p>
    <p
      class="mt-2 text-[22px] font-semibold leading-tight tracking-[-0.01em]"
      :class="[toneClass[tone], mono ? 'font-mono-data' : '']"
    >
      {{ value }}
    </p>
    <p
      v-if="hint"
      class="mt-1 text-xs text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
  </div>
</template>
