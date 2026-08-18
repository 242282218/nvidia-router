<script setup lang="ts">
import { computed } from 'vue'

import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

// 按钮原语：variant 表达语义，size 表达密度，loading 接管禁用与等待反馈。
// 视图中不再出现裸 <button class="btn-…">，保证全站按钮行为一致。
defineOptions({ name: 'UiButton' })

const props = withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'md' | 'sm'
  type?: 'button' | 'submit' | 'reset'
  /** 等待态：显示 spinner、阻止重复提交。 */
  loading?: boolean
  disabled?: boolean
  /** 可选前置图标（集中图标注册表）。 */
  icon?: IconName
  /** 块级占满父宽（登录等单列表单主操作）。 */
  block?: boolean
  loadingLabel?: string
}>(), {
  variant: 'secondary',
  size: 'md',
  type: 'button',
  loading: false,
  disabled: false,
  icon: undefined,
  block: false,
  loadingLabel: undefined,
})

const emit = defineEmits<{ click: [event: globalThis.MouseEvent] }>()

const classes = computed(() => {
  const base = `btn-${props.variant}`
  const size = props.size === 'sm' ? 'btn-sm' : ''
  const block = props.block ? 'w-full' : ''
  return [base, size, block].filter(Boolean).join(' ')
})

function onClick(event: globalThis.MouseEvent): void {
  if (props.loading || props.disabled) {
    event.preventDefault()
    return
  }
  emit('click', event)
}
</script>

<template>
  <button
    :class="classes"
    :type="type"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    @click="onClick"
  >
    <svg
      v-if="loading"
      class="h-3.5 w-3.5 animate-spin"
      fill="none"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <circle
        class="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        stroke-width="4"
      />
      <path
        class="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
    <UiIcon
      v-else-if="icon"
      :name="icon"
    />
    <span v-if="loading && loadingLabel">{{ loadingLabel }}</span>
    <slot v-else />
  </button>
</template>
