<script setup lang="ts">
// 状态徽章：圆点 + 文案双编码（颜色永不单独承载状态）。
defineOptions({ name: 'UiBadge' })

// Variant → shortcut must stay a literal record: UnoCSS extracts class names
// from raw file text, so a `badge-${variant}` template literal would hide
// variants whose shortcut has no other literal occurrence in the codebase.
const variantClass = {
  success: 'badge-success',
  warning: 'badge-warning',
  danger: 'badge-danger',
  muted: 'badge-muted',
  info: 'badge-info',
} as const

withDefaults(defineProps<{
  variant: keyof typeof variantClass
  label: string
  dot?: boolean
}>(), { dot: true })
</script>

<template>
  <span :class="variantClass[variant]">
    <span
      v-if="dot"
      class="h-1.5 w-1.5 shrink-0 rounded-full bg-current"
      aria-hidden="true"
    />
    {{ label }}
  </span>
</template>
