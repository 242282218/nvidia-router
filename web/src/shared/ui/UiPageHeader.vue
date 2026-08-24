<script setup lang="ts">
// 页面头部：眉题（分组语境）+ h1 + 副标题 + 操作区。
// 与侧栏导航分组共用同一套分组名，用户始终知道自己在哪。
defineOptions({ name: 'UiPageHeader' })

withDefaults(defineProps<{
  eyebrow?: string
  title: string
  subtitle?: string
}>(), { eyebrow: undefined, subtitle: undefined })
</script>

<template>
  <header class="section-header">
    <div class="min-w-0">
      <p
        v-if="eyebrow"
        class="type-label"
      >
        {{ eyebrow }}
      </p>
      <h1 class="type-title mt-1.5">
        {{ title }}
      </h1>
      <p
        v-if="subtitle"
        class="page-subtitle"
      >
        {{ subtitle }}
      </p>
    </div>
    <div
      v-if="$slots.actions"
      class="flex flex-wrap items-center gap-2"
    >
      <slot name="actions" />
    </div>
  </header>
</template>
