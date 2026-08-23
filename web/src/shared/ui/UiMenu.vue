<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { AnimatePresence, Motion, useReducedMotion } from 'motion-v'

import UiIcon from './UiIcon.vue'
import { springSoft } from '../motion'
import { horizontalPlacement, type HorizontalPlacement } from './overlayPosition'

// 轻量菜单原语：图标触发钮 + 上浮/下挂面板（surface-raised 柔影）。
// 交互契约：Esc 关闭并归还焦点到触发钮；点击面板外关闭；reduced-motion 直切。
// 菜单项由调用方以 role="menuitem" 按钮通过默认 slot 提供，slot 接收 close()。
defineOptions({ name: 'UiMenu' })

const props = withDefaults(defineProps<{
  /** 无障碍名称：同时作为触发钮 aria-label 与面板 aria-label。 */
  label: string
  /** 弹出方位：top-start 自下而上左对齐（侧栏底部）；bottom-end 下挂右对齐（页内工具行）。 */
  placement?: 'top-start' | 'bottom-end'
  /** 触发钮样式类；缺省为标准 36px 图标钮。 */
  triggerClass?: string
}>(), { placement: 'top-start', triggerClass: undefined })

const open = ref(false)
const rootRef = ref<globalThis.HTMLElement | null>(null)
const buttonRef = ref<globalThis.HTMLButtonElement | null>(null)
const horizontalSide = ref<Extract<HorizontalPlacement, 'start' | 'end'>>('start')
const reducedMotion = useReducedMotion()

function toggle(): void {
  if (!open.value) updateHorizontalSide()
  open.value = !open.value
}

function close(returnFocus = true): void {
  if (!open.value) return
  open.value = false
  if (returnFocus) buttonRef.value?.focus()
}

// 点击面板外任意处关闭；不归还焦点（用户已把注意力移去别处）。
function onDocumentPointerDown(event: globalThis.PointerEvent): void {
  const root = rootRef.value
  if (!open.value || !root) return
  if (event.target instanceof globalThis.Node && !root.contains(event.target)) close(false)
}

function updateHorizontalSide(): void {
  const root = rootRef.value
  if (!root) return
  const rect = root.getBoundingClientRect()
  const edge = horizontalPlacement(rect.left + rect.width / 2, globalThis.innerWidth)
  horizontalSide.value = props.placement === 'top-start'
    ? edge === 'end' ? 'end' : 'start'
    : edge === 'start' ? 'start' : 'end'
}

onMounted(() => {
  globalThis.document.addEventListener('pointerdown', onDocumentPointerDown, true)
  globalThis.addEventListener('resize', updateHorizontalSide)
})

onBeforeUnmount(() => {
  globalThis.document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  globalThis.removeEventListener('resize', updateHorizontalSide)
})

const panelClass = computed(() => props.placement === 'bottom-end'
  ? horizontalSide.value === 'end'
    ? 'top-full right-0 mt-2 origin-top-right'
    : 'top-full left-0 mt-2 origin-top-left'
  : horizontalSide.value === 'end'
    ? 'bottom-full right-0 mb-2 origin-bottom-right'
    : 'bottom-full left-0 mb-2 origin-bottom-left')

const enterFrom = computed(() => props.placement === 'bottom-end'
  ? { opacity: 0, scale: 0.96, y: -4 }
  : { opacity: 0, scale: 0.96, y: 4 })
</script>

<template>
  <div
    ref="rootRef"
    class="relative"
    @keydown.escape.stop="close()"
  >
    <button
      ref="buttonRef"
      type="button"
      class="inline-flex shrink-0 items-center justify-center rounded-[var(--radius-control)] text-[var(--color-text-subtle)] transition-[background-color,color] duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:text-[var(--color-disabled-foreground)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11 pointer-coarse:w-11"
      :class="triggerClass ?? 'icon-btn-sm'"
      :aria-label="label"
      :title="label"
      aria-haspopup="menu"
      :aria-expanded="open"
      @click="toggle"
    >
      <!-- @slot 触发钮视觉内容；缺省为省略号图标 -->
      <slot name="trigger">
        <UiIcon
          name="more"
          :size="16"
        />
      </slot>
    </button>
    <AnimatePresence>
      <Motion
        v-if="open"
        tag="div"
        role="menu"
        :aria-label="label"
        class="absolute z-50 min-w-[188px] max-w-[calc(100vw-2rem)] max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-1"
        :class="panelClass"
        :initial="reducedMotion ? { opacity: 0 } : enterFrom"
        :animate="{ opacity: 1, scale: 1, y: 0 }"
        :exit="{ opacity: 0, scale: 0.98 }"
        :transition="reducedMotion ? { duration: 0 } : springSoft"
      >
        <!--
          @slot 菜单主体：放置 role="menuitem" 的按钮/链接，
          点击后调用 slot props 的 close() 收起并归还焦点。
        -->
        <slot :close="close" />
      </Motion>
    </AnimatePresence>
  </div>
</template>
