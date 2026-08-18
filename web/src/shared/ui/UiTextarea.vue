<script setup lang="ts">
// 多行输入原语：批量导入等场景，默认 6 行、可纵向拉伸。
defineOptions({ name: 'UiTextarea', inheritAttrs: false })

withDefaults(defineProps<{
  rows?: number
  mono?: boolean
}>(), { rows: 6, mono: false })

const model = defineModel<string>({ default: '' })
</script>

<template>
  <textarea
    v-model="model"
    class="input-field h-auto min-h-[4.5rem] resize-y py-2.5 leading-relaxed"
    :class="mono ? 'font-mono-data text-[13px]' : ''"
    :rows="rows"
    v-bind="$attrs"
  />
</template>
