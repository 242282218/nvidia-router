<script setup lang="ts">
// 文本输入原语：v-model 直通，类型/占位/属性透传到底层 <input>。
defineOptions({ name: 'UiInput', inheritAttrs: false })

withDefaults(defineProps<{
  type?: string
  /** 等宽数字/代码样式（Key、ID、金额等数据字段）。 */
  mono?: boolean
}>(), { type: 'text', mono: false })

const model = defineModel<string>({ default: '' })
</script>

<template>
  <input
    v-model="model"
    class="input-field"
    :class="mono ? 'font-mono-data' : ''"
    :type="type"
    v-bind="$attrs"
  >
</template>
