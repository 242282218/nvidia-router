<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { horizontalPlacement, type HorizontalPlacement } from './overlayPosition'

defineOptions({ name: 'UiTooltip' })

const props = withDefaults(defineProps<{
  text: string
  placement?: 'top' | 'bottom'
}>(), { placement: 'top' })

const visible = ref(false)
const hostRef = ref<globalThis.HTMLElement | null>(null)
const horizontalSide = ref<HorizontalPlacement>('center')
const actualPlacement = ref<'top' | 'bottom'>('top')
const tooltipStyle = ref<Record<string, string>>({})
let timer: ReturnType<typeof setTimeout> | undefined

function updatePosition(): void {
  const host = hostRef.value
  if (!host) return
  const rect = host.getBoundingClientRect()
  const viewportWidth = globalThis.innerWidth
  const viewportHeight = globalThis.innerHeight
  horizontalSide.value = horizontalPlacement(rect.left + rect.width / 2, viewportWidth)
  actualPlacement.value = props.placement === 'top' && rect.top < 56
    ? 'bottom'
    : props.placement === 'bottom' && rect.bottom > viewportHeight - 56
      ? 'top'
      : props.placement

  const edge = 12
  const anchor = horizontalSide.value === 'start'
    ? rect.left
    : horizontalSide.value === 'end'
      ? rect.right
      : rect.left + rect.width / 2
  const left = Math.min(Math.max(anchor, edge), Math.max(edge, viewportWidth - edge))
  const top = actualPlacement.value === 'top' ? rect.top - 8 : rect.bottom + 8
  const translateX = horizontalSide.value === 'start'
    ? '0'
    : horizontalSide.value === 'end'
      ? '-100%'
      : '-50%'
  const translateY = actualPlacement.value === 'top' ? '-100%' : '0'
  tooltipStyle.value = {
    left: `${left}px`,
    top: `${Math.max(edge, Math.min(top, viewportHeight - edge))}px`,
    transform: `translate(${translateX}, ${translateY})`,
  }
}

function show(): void {
  clearTimeout(timer)
  updatePosition()
  timer = setTimeout(() => { visible.value = true }, 350)
}

function hide(): void {
  clearTimeout(timer)
  visible.value = false
}

onMounted(() => {
  globalThis.addEventListener('resize', updatePosition)
  globalThis.addEventListener('scroll', updatePosition, true)
})

onBeforeUnmount(() => {
  clearTimeout(timer)
  globalThis.removeEventListener('resize', updatePosition)
  globalThis.removeEventListener('scroll', updatePosition, true)
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
  </span>
  <Teleport to="body">
    <Transition name="tooltip">
      <span
        v-if="visible"
        class="tooltip-motion tooltip-surface pointer-events-none fixed flex w-max"
        :style="tooltipStyle"
        role="tooltip"
      >{{ text }}</span>
    </Transition>
  </Teleport>
</template>

<style scoped>
.tooltip-enter-active,
.tooltip-leave-active {
  transition: opacity var(--duration-micro) var(--ease-enter), transform var(--duration-micro) var(--ease-enter);
}
.tooltip-enter-from {
  opacity: 0;
}
.tooltip-leave-to {
  opacity: 0;
}
</style>
