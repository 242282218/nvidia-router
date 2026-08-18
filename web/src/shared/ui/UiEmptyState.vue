<script setup lang="ts">
import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

// 空状态：说明「这里会展示什么」+ 给出下一步动作，而不只是「暂无数据」。
defineOptions({ name: 'UiEmptyState' })

withDefaults(defineProps<{
  icon?: IconName
  title: string
  hint?: string
}>(), { icon: 'inbox', hint: undefined })
</script>

<template>
  <div class="flex flex-col items-center px-6 py-12 text-center">
    <div
      class="flex h-11 w-11 items-center justify-center rounded-full bg-[var(--color-sunken)] text-[var(--color-text-subtle)]"
      aria-hidden="true"
    >
      <UiIcon
        :name="icon"
        :size="20"
      />
    </div>
    <p class="mt-3 text-sm font-medium text-[var(--color-text-secondary)]">
      {{ title }}
    </p>
    <p
      v-if="hint"
      class="mt-1 max-w-sm text-xs leading-relaxed text-[var(--color-text-muted)]"
    >
      {{ hint }}
    </p>
    <div
      v-if="$slots.default"
      class="mt-4"
    >
      <slot />
    </div>
  </div>
</template>
