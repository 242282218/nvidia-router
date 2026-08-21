<script setup lang="ts">
import { computed } from 'vue'

// 环形进度/健康度仪表：SVG stroke-dasharray 实现，数值变化走 CSS 过渡。
defineOptions({ name: 'UiProgressRing' })

const props = withDefaults(defineProps<{
  /** 0–100。 */
  value: number
  /** 像素直径。 */
  size?: number
  strokeWidth?: number
  tone?: 'success' | 'warning' | 'danger' | 'info' | 'accent'
  /** 中心文字；缺省显示整数值。 */
  label?: string
}>(), {
  size: 96,
  strokeWidth: 8,
  tone: 'accent',
  label: undefined,
})

const radius = computed(() => (props.size - props.strokeWidth) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)
const clamped = computed(() => Math.min(100, Math.max(0, props.value)))
const dashOffset = computed(() => circumference.value * (1 - clamped.value / 100))

const toneVar = computed(() => `var(--color-${props.tone === 'accent' ? 'text' : props.tone})`)
</script>

<template>
  <div
    class="relative inline-flex items-center justify-center"
    role="img"
    :aria-label="`${label ?? Math.round(clamped)}：${Math.round(clamped)}%`"
  >
    <svg
      :width="size"
      :height="size"
      :viewBox="`0 0 ${size} ${size}`"
      fill="none"
    >
      <circle
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        :stroke="'var(--color-border-subtle)'"
        :stroke-width="strokeWidth"
      />
      <circle
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        :stroke="toneVar"
        :stroke-width="strokeWidth"
        stroke-linecap="round"
        :stroke-dasharray="circumference"
        :stroke-dashoffset="dashOffset"
        transform="rotate(-90)"
        :style="{ transformOrigin: `${size / 2}px ${size / 2}px`, transition: 'stroke-dashoffset var(--duration-overlay) var(--ease-enter)' }"
      />
    </svg>
    <div class="absolute inset-0 flex flex-col items-center justify-center">
      <span class="font-mono-data text-lg font-semibold leading-none text-[var(--color-text)]">
        {{ label ?? Math.round(clamped) }}
      </span>
      <slot name="sub" />
    </div>
  </div>
</template>
