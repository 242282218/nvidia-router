<script setup lang="ts">
import { computed } from 'vue'

import { icons, type IconName } from './icons'

// 全应用唯一的图标出口：任何图标都经由 <UiIcon name="…" /> 渲染，
// 底层为 @lucide/vue 组件（24×24 线性）。尺寸默认跟随字号（1em），
// 描边统一 1.5 保持暖纸的精细线条。
defineOptions({ name: 'UiIcon' })

const props = withDefaults(defineProps<{
  name: IconName
  /** 像素尺寸；缺省 1em，随父级 font-size 缩放。 */
  size?: number | string
  /** 有语义时应给标签；纯装饰保持缺省（aria-hidden）。 */
  label?: string
}>(), { size: undefined, label: undefined })

const icon = computed(() => icons[props.name])

const dimension = computed(() => {
  if (props.size === undefined) return '1em'
  return typeof props.size === 'number' ? `${props.size}px` : props.size
})
</script>

<template>
  <component
    :is="icon"
    class="inline-block shrink-0 align-[-0.125em]"
    :style="{ width: dimension, height: dimension }"
    :stroke-width="1.5"
    :aria-hidden="label ? undefined : true"
    :aria-label="label"
    :role="label ? 'img' : undefined"
  />
</template>
