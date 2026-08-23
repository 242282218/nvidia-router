<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { horizontalPlacement, type HorizontalPlacement } from './overlayPosition'

// 轻量浮动提示：hover / focus 延迟 350ms 出现，纯 CSS 定位（无浮层库）。
// 触发器必须是单个可聚焦元素（按钮/链接），由默认插槽传入。
defineOptions({ name: 'UiTooltip' })

withDefaults(defineProps<{
  text: string
  placement?: 'top' | 'bottom'
}>(), { placement: 'top' })

const visible = ref(false)
const hostRef = ref<globalThis.HTMLElement | null>(null)
const horizontalSide = ref<HorizontalPlacement>('center')
let timer: ReturnType<typeof setTimeout> | undefined

function updateHorizontalSide(): void {
  const host = hostRef.value
  if (!host) return
  const rect = host.getBoundingClientRect()
  horizontalSide.value = horizontalPlacement(rect.left + rect.width / 2, globalThis.innerWidth)
}

function show(): void {
  clearTimeout(timer)
  updateHorizontalSide()
  timer = setTimeout(() => { visible.value = true }, 350)
}

function hide(): void {
  clearTimeout(timer)
  visible.value = false
}

onMounted(() => {
  globalThis.addEventListener('resize', updateHorizontalSide)
})

onBeforeUnmount(() => {
  clearTimeout(timer)
  globalThis.removeEventListener('resize', updateHorizontalSide)
})
</script>

<template>
  <span
    ref="hostRef"
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
        class="pointer-events-none absolute z-[70] flex max-w-[calc(100vw-2rem)]"
        :class="[
          placement === 'top' ? 'bottom-[calc(100%+6px)]' : 'top-[calc(100%+6px)]',
          horizontalSide === 'start' ? 'left-0' : horizontalSide === 'end' ? 'right-0' : 'left-1/2 -translate-x-1/2',
        ]"
      >
        <span
          class="tooltip-motion tooltip-surface w-max"
          role="tooltip"
        >{{ text }}</span>
      </span>
    </Transition>
  </span>
</template>

<style scoped>
.tooltip-enter-active .tooltip-motion {
  transition: opacity var(--duration-micro) var(--ease-enter), transform var(--duration-micro) var(--ease-enter);
}
.tooltip-leave-active .tooltip-motion {
  transition: opacity var(--duration-micro) var(--ease-exit);
}
.tooltip-enter-from .tooltip-motion {
  opacity: 0;
  transform: translateY(4px);
}
.tooltip-leave-to .tooltip-motion {
  opacity: 0;
}
</style>
