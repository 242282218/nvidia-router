<script setup lang="ts">
import { computed } from 'vue'
import { Motion, useReducedMotion } from 'motion-v'

import UiIcon from '../../shared/ui/UiIcon.vue'
import { springSoft } from '../../shared/motion'

// 认证页共享骨架：扁平风格（纯色底 + 实色卡片）。
// 登录与强制改密两个场景视觉完全同构，骨架只维护这一份。
defineOptions({ name: 'AuthLayout' })

withDefaults(defineProps<{
  title: string
  subtitle: string
  /** 品牌徽标底色场景：默认品牌绿；强制改密用警示色提醒「必须先完成这一步」。 */
  badgeTone?: 'brand' | 'warning'
  badgeText?: string
}>(), { badgeTone: 'brand', badgeText: 'N' })

const isPlainHttp = computed(() => globalThis.location.protocol === 'http:')
const reducedMotion = useReducedMotion()
const entrance = computed(() => (reducedMotion.value ? { duration: 0 } : springSoft))
</script>

<template>
  <div class="relative flex min-h-dvh items-center justify-center px-4 py-10">
    <Motion
      tag="section"
      class="relative w-full max-w-sm"
      :initial="reducedMotion ? { opacity: 0 } : { opacity: 0, y: 12 }"
      :animate="{ opacity: 1, y: 0 }"
      :transition="entrance"
    >
      <div class="mb-8 text-center">
        <div
          class="relative mx-auto flex h-14 w-14 items-center justify-center rounded-[var(--radius-panel)] text-lg font-bold"
          :class="badgeTone === 'brand'
            ? 'bg-[var(--color-brand)] text-[var(--color-brand-foreground)]'
            : 'bg-[var(--color-warning)] text-[var(--color-canvas)]'"
        >
          {{ badgeText }}
        </div>
        <h1 class="type-title mt-5">
          {{ title }}
        </h1>
        <p class="mt-1.5 text-sm text-[var(--color-text-muted)]">
          {{ subtitle }}
        </p>
        <slot name="brand" />
      </div>

      <div
        v-if="isPlainHttp"
        class="mb-6 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] bg-[var(--color-warning-background)] p-3 text-sm text-[var(--color-warning)]"
        role="alert"
      >
        <div class="flex items-start gap-2">
          <UiIcon
            name="warning"
            :size="16"
            class="mt-0.5 shrink-0"
          />
          <span>当前页面使用 HTTP，账号和密码会通过明文连接传输。请仅在可信网络中操作。</span>
        </div>
      </div>

      <!-- 实色卡片：靠描边与底色差分层 -->
      <div class="relative rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-elevated)] p-6">
        <slot />
      </div>
    </Motion>
  </div>
</template>
