<script setup lang="ts">
// 卡片容器原语：统一的标题区（title / subtitle / actions）+ 内容区。
// padding 与标题层级集中在这里，视图只关心业务内容。
defineOptions({ name: 'UiCard' })

withDefaults(defineProps<{
  title?: string
  subtitle?: string
  /** 关闭默认内边距（表格等需要贴边的场景）。 */
  padded?: boolean
  /** 可交互上浮效果（仅真正可点的卡片使用）。 */
  interactive?: boolean
}>(), {
  title: undefined,
  subtitle: undefined,
  padded: true,
  interactive: false,
})
</script>

<template>
  <section :class="[interactive ? 'card-hover' : 'card', padded ? 'p-6' : '']">
    <header
      v-if="title || $slots.actions"
      class="mb-5 flex flex-wrap items-start justify-between gap-3"
      :class="padded ? '' : 'px-6 pt-6'"
    >
      <div class="min-w-0">
        <h2 class="type-heading">
          {{ title }}
        </h2>
        <p
          v-if="subtitle"
          class="mt-0.5 text-xs text-[var(--color-text-muted)]"
        >
          {{ subtitle }}
        </p>
      </div>
      <div
        v-if="$slots.actions"
        class="flex shrink-0 flex-wrap items-center gap-2"
      >
        <slot name="actions" />
      </div>
    </header>
    <slot />
  </section>
</template>
