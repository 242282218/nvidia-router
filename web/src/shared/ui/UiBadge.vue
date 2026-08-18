<script setup lang="ts">
// 状态徽章：圆点 + 文案双编码（颜色永不单独承载状态）。
defineOptions({ name: 'UiBadge' })

withDefaults(defineProps<{
  variant: 'success' | 'warning' | 'danger' | 'muted' | 'info'
  label: string
  dot?: boolean
}>(), { dot: true })
</script>

<template>
  <span :class="`badge-${variant}`">
    <span
      v-if="dot"
      class="h-1.5 w-1.5 shrink-0 rounded-full bg-current"
      aria-hidden="true"
    />
    {{ label }}
  </span>
</template>
