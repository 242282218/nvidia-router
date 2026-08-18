<script setup lang="ts">
// 骨架屏：加载等待期间给出内容轮廓，替代 spinner（布局不跳变、感知更快）。
defineOptions({ name: 'UiSkeleton' })

withDefaults(defineProps<{
  /** text 若干行 / table 若干行 / cards 若干块。 */
  variant?: 'text' | 'table' | 'cards'
  lines?: number
}>(), { variant: 'text', lines: 4 })
</script>

<template>
  <div
    aria-hidden="true"
    class="animate-fade-in"
  >
    <div
      v-if="variant === 'text'"
      class="space-y-3"
    >
      <div
        v-for="i in lines"
        :key="i"
        class="skeleton h-3.5"
        :style="{ width: `${100 - ((i - 1) % 3) * 18}%` }"
      />
    </div>

    <div
      v-else-if="variant === 'table'"
      class="card overflow-hidden"
    >
      <div class="border-b border-[var(--color-border)] px-4 py-3.5">
        <div class="skeleton h-3 w-2/5" />
      </div>
      <div
        v-for="i in lines"
        :key="i"
        class="flex items-center gap-4 border-b border-[var(--color-border-subtle)] px-4 py-4 last:border-b-0"
      >
        <div class="skeleton h-3.5 flex-[2]" />
        <div class="skeleton h-3.5 flex-[3]" />
        <div class="skeleton h-3.5 flex-1" />
        <div class="skeleton h-6 w-16 rounded-full" />
      </div>
    </div>

    <div
      v-else
      class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"
    >
      <div
        v-for="i in lines"
        :key="i"
        class="card space-y-3 p-5"
      >
        <div class="skeleton h-3.5 w-1/3" />
        <div class="skeleton h-6 w-2/3" />
        <div class="skeleton h-3 w-1/2" />
      </div>
    </div>
  </div>
</template>
