<script setup lang="ts">
// 开关：二元状态（启用/停用）的直接操作，替代「点按钮 → 等刷新」的间接路径。
// 状态由父级单向传入（真实来源是服务端），change 事件请求变更；
// 忙碌或失败时父级不更新 checked，开关自动回到真实状态。
defineOptions({ name: 'UiSwitch' })

withDefaults(defineProps<{
  checked: boolean
  disabled?: boolean
  label?: string
}>(), { disabled: false, label: undefined })

const emit = defineEmits<{ change: [value: boolean] }>()
</script>

<template>
  <button
    class="inline-flex h-6 w-11 shrink-0 items-center rounded-full border p-0.5 transition-[background-color,border-color] duration-[var(--duration-micro)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 disabled:cursor-not-allowed"
    :class="[
      checked ? 'justify-end' : 'justify-start',
      disabled
        ? 'border-[var(--color-disabled-border)] bg-[var(--color-disabled-background)]'
        : checked
          ? 'border-[var(--color-success)] bg-[var(--color-success)]'
          : 'border-[var(--color-border-strong)] bg-[var(--color-sunken)]',
    ]"
    type="button"
    role="switch"
    :aria-checked="checked"
    :aria-label="label"
    :disabled="disabled"
    @click="emit('change', !checked)"
  >
    <span
      class="h-5 w-5 rounded-full"
      :class="disabled ? 'bg-[var(--color-disabled-foreground)]' : 'bg-white'"
      aria-hidden="true"
    />
  </button>
</template>
