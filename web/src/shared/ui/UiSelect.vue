<script setup lang="ts">
import UiIcon from './UiIcon.vue'

// 下拉选择原语：统一样式与自定义 chevron，选项经 slot 传入原生 <option>。
defineOptions({ name: 'UiSelect', inheritAttrs: false })

const model = defineModel<string>({ default: '' })
</script>

<template>
  <div class="relative">
    <select
      v-model="model"
      class="input-field cursor-pointer appearance-none pr-9"
      v-bind="$attrs"
    >
      <slot />
    </select>
    <UiIcon
      name="chevron-down"
      class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[var(--color-text-subtle)]"
    />
  </div>
</template>
