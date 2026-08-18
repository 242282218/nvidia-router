<script setup lang="ts">
import { computed } from 'vue'

import { icons, type IconName } from './icons'

// 全应用唯一的图标出口：任何图标都经由 <UiIcon name="…" /> 渲染，
// 不再手写内联 SVG。尺寸默认跟随字号（1em），可用 size 覆盖。
defineOptions({ name: 'UiIcon' })

const props = withDefaults(defineProps<{
  name: IconName
  /** 像素尺寸；缺省 1em，随父级 font-size 缩放。 */
  size?: number | string
  /** 有语义时应给标签；纯装饰保持缺省（aria-hidden）。 */
  label?: string
}>(), { size: undefined, label: undefined })

const paths = computed<readonly string[]>(() => icons[props.name] ?? [])

const dimension = computed(() => {
  if (props.size === undefined) return '1em'
  return typeof props.size === 'number' ? `${props.size}px` : props.size
})
</script>

<template>
  <svg
    class="inline-block shrink-0 align-[-0.125em]"
    :style="{ width: dimension, height: dimension }"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    viewBox="0 0 24 24"
    stroke-linecap="round"
    stroke-linejoin="round"
    :aria-hidden="label ? undefined : 'true'"
    :aria-label="label"
    :role="label ? 'img' : undefined"
  >
    <path
      v-for="d in paths"
      :key="d"
      :d="d"
    />
  </svg>
</template>
