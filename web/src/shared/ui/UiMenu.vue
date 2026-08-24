<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import UiIcon from './UiIcon.vue'
import { horizontalPlacement, type HorizontalPlacement } from './overlayPosition'

defineOptions({ name: 'UiMenu' })

const props = withDefaults(defineProps<{
  label: string
  placement?: 'top-start' | 'bottom-end'
  triggerClass?: string
}>(), { placement: 'top-start', triggerClass: undefined })

const open = ref(false)
const rootRef = ref<globalThis.HTMLElement | null>(null)
const buttonRef = ref<globalThis.HTMLButtonElement | null>(null)
const panelRef = ref<globalThis.HTMLElement | null>(null)
const horizontalSide = ref<Extract<HorizontalPlacement, 'start' | 'end'>>('start')
const panelStyle = ref<Record<string, string>>({})
const MENU_FOCUSABLE_SELECTOR = [
  '[role="menuitem"]:not([aria-disabled="true"]):not([disabled])',
  'button:not([disabled])',
  'a[href]',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ')

function toggle(): void {
  if (!open.value) updatePosition()
  open.value = !open.value
}

function close(returnFocus = true): void {
  if (!open.value) return
  open.value = false
  if (returnFocus) buttonRef.value?.focus()
}

function onDocumentPointerDown(event: globalThis.PointerEvent): void {
  const root = rootRef.value
  if (!open.value || !root || !(event.target instanceof globalThis.Node)) return
  if (root.contains(event.target) || panelRef.value?.contains(event.target)) return
  close(false)
}

function onDocumentKeydown(event: globalThis.KeyboardEvent): void {
  if (!open.value || event.key !== 'Escape') return
  event.preventDefault()
  close()
}

function menuFocusableElements(): globalThis.HTMLElement[] {
  const panel = panelRef.value
  if (!panel) return []
  return Array.from(panel.querySelectorAll<globalThis.HTMLElement>(MENU_FOCUSABLE_SELECTOR))
}

function onPanelKeydown(event: globalThis.KeyboardEvent): void {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const items = menuFocusableElements()
  if (items.length === 0) return

  const active = globalThis.document.activeElement
  const currentIndex = active instanceof globalThis.HTMLElement ? items.indexOf(active) : -1
  const nextIndex = event.key === 'Home'
    ? 0
    : event.key === 'End'
      ? items.length - 1
      : event.key === 'ArrowDown'
        ? currentIndex < 0 || currentIndex === items.length - 1 ? 0 : currentIndex + 1
        : currentIndex <= 0 ? items.length - 1 : currentIndex - 1

  event.preventDefault()
  items[nextIndex]?.focus()
}

function updatePosition(): void {
  const trigger = buttonRef.value
  if (!trigger) return
  const rect = trigger.getBoundingClientRect()
  const edge = horizontalPlacement(rect.left + rect.width / 2, globalThis.innerWidth)
  horizontalSide.value = props.placement === 'top-start'
    ? edge === 'end' ? 'end' : 'start'
    : edge === 'start' ? 'start' : 'end'

  const panel = panelRef.value
  const panelRect = panel?.getBoundingClientRect()
  const panelWidth = panelRect?.width || 188
  const panelHeight = panelRect?.height || 0
  const gap = 8
  const left = horizontalSide.value === 'end' ? rect.right - panelWidth : rect.left
  const top = props.placement === 'top-start'
    ? rect.top - panelHeight - gap
    : rect.bottom + gap
  const maxLeft = Math.max(8, globalThis.innerWidth - panelWidth - 8)
  const maxTop = Math.max(8, globalThis.innerHeight - panelHeight - 8)
  panelStyle.value = {
    left: `${Math.min(Math.max(8, left), maxLeft)}px`,
    top: `${Math.min(Math.max(8, top), maxTop)}px`,
  }
}

onMounted(() => {
  globalThis.document.addEventListener('pointerdown', onDocumentPointerDown, true)
  globalThis.document.addEventListener('keydown', onDocumentKeydown, true)
  globalThis.addEventListener('resize', updatePosition)
  globalThis.addEventListener('scroll', updatePosition, true)
})

onBeforeUnmount(() => {
  globalThis.document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  globalThis.document.removeEventListener('keydown', onDocumentKeydown, true)
  globalThis.removeEventListener('resize', updatePosition)
  globalThis.removeEventListener('scroll', updatePosition, true)
})

watch(open, (isOpen) => {
  if (!isOpen) return
  void nextTick(() => {
    updatePosition()
    if (!open.value) return
    ;(menuFocusableElements()[0] ?? panelRef.value)?.focus()
  })
})
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
      <slot name="trigger">
        <UiIcon
          name="more"
          :size="16"
        />
      </slot>
    </button>
  </div>
  <Teleport to="body">
    <Transition name="menu">
      <div
        v-if="open"
        ref="panelRef"
        role="menu"
        tabindex="-1"
        :aria-label="label"
        :data-placement="placement"
        class="fixed z-[var(--z-popover)] min-w-[188px] max-w-[calc(100vw-2rem)] max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface-raised)] p-1"
        :style="panelStyle"
        @keydown.escape.stop="close()"
        @keydown="onPanelKeydown"
      >
        <slot :close="close" />
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.menu-enter-active,
.menu-leave-active {
  transition: opacity var(--duration-micro) var(--ease-enter), transform var(--duration-micro) var(--ease-enter);
}

.menu-enter-from[data-placement='top-start'] {
  opacity: 0;
  transform: translateY(4px) scale(0.98);
}

.menu-enter-from[data-placement='bottom-end'] {
  opacity: 0;
  transform: translateY(-4px) scale(0.98);
}

.menu-leave-to {
  opacity: 0;
  transform: scale(0.98);
}
</style>
