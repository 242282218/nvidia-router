<script setup lang="ts">
// 表单项容器：label + 控件 + hint/error 的统一排版与可访问性接线。
// error 与 hint 通过 aria-describedby 关联，错误同时以 role="alert" 播报。
defineOptions({ name: 'UiField' })

withDefaults(defineProps<{
  label?: string
  /** 关联控件的 id；提供时 label 渲染为 <label for>。 */
  inputId?: string
  hint?: string
  error?: string
  required?: boolean
}>(), {
  label: undefined,
  inputId: undefined,
  hint: undefined,
  error: undefined,
  required: false,
})
</script>

<template>
  <div>
    <label
      v-if="label"
      class="field-label"
      :for="inputId"
    >
      {{ label }}
      <span
        v-if="required"
        class="text-[var(--color-danger)]"
        aria-hidden="true"
      >*</span>
    </label>
    <slot />
    <p
      v-if="error"
      class="mt-1.5 text-xs text-[var(--color-danger)]"
      role="alert"
    >
      {{ error }}
    </p>
    <p
      v-else-if="hint"
      class="mt-1.5 text-xs text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
  </div>
</template>
