<script setup lang="ts">
import { ref } from 'vue'

// 轻量浮动提示：hover / focus 延迟 350ms 出现，纯 CSS 定位（无浮层库）。
// 触发器必须是单个可聚焦元素（按钮/链接），由默认插槽传入。
defineOptions({ name: 'UiTooltip' })

withDefaults(defineProps<{
  text: string
  placement?: 'top' | 'bottom'
}>(), { placement: 'top' })

const visible = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

function show(): void {
  clearTimeout(timer)
  timer = setTimeout(() => { visible.value = true }, 350)
}

function hide(): void {
  clearTimeout(timer)
  visible.value = false
}
</script>

<template>
  <span
    class="relative inline-flex"
    @mouseenter="show"
    @mouseleave="hide"
    @focusin="show"
    @focusout="hide"
  >
    <slot />
    <Transition name="tooltip">
      <!-- 外层只负责水平居中与纵向定位，内层承载动画，避免 transform 冲突 -->
      <span
        v-if="visible"
        class="pointer-events-none absolute inset-x-0 flex justify-center"
        :class="placement === 'top' ? 'bottom-[calc(100%+6px)]' : 'top-[calc(100%+6px)]'"
      >
        <span
          class="tooltip-surface w-max"
          role="tooltip"
        >{{ text }}</span>
      </span>
    </Transition>
  </span>
</template>

<style scoped>
.tooltip-enter-active {
  transition: opacity var(--duration-micro) var(--ease-enter), transform var(--duration-micro) var(--ease-enter);
}
.tooltip-leave-active {
  transition: opacity var(--duration-micro) var(--ease-exit);
}
.tooltip-enter-from {
  opacity: 0;
  transform: translateY(4px);
}
.tooltip-leave-to {
  opacity: 0;
}
</style>
