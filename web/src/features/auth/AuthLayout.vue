<script setup lang="ts">
import { computed } from 'vue'

import UiIcon from '../../shared/ui/UiIcon.vue'

// 认证页共享骨架：暖光氛围、品牌区、HTTP 明文警示、居中卡片。
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
</script>

<template>
  <div class="flex min-h-screen items-center justify-center px-4 py-10">
    <!-- Ambient decoration: 暖光 + 一点品牌绿，极淡，仅作氛围 -->
    <div class="pointer-events-none fixed inset-0 overflow-hidden">
      <div class="absolute -top-40 -right-40 h-96 w-96 rounded-full bg-[rgba(255,216,150,0.30)] blur-3xl" />
      <div class="absolute -bottom-40 -left-40 h-96 w-96 rounded-full bg-[rgba(118,185,0,0.10)] blur-3xl" />
    </div>

    <section class="relative w-full max-w-sm animate-fade-in">
      <div class="mb-8 text-center">
        <div
          class="mx-auto flex h-12 w-12 items-center justify-center rounded-[14px] text-lg font-bold shadow-[var(--shadow-sm)]"
          :class="badgeTone === 'brand'
            ? 'bg-[var(--color-brand)] text-[var(--color-brand-foreground)]'
            : 'bg-[var(--color-warning)] text-[var(--color-canvas)]'"
        >
          {{ badgeText }}
        </div>
        <h1 class="mt-4 text-lg font-semibold tracking-[-0.01em] text-[var(--color-text)]">
          {{ title }}
        </h1>
        <p class="mt-1 text-sm text-[var(--color-text-muted)]">
          {{ subtitle }}
        </p>
        <slot name="brand" />
      </div>

      <div
        v-if="isPlainHttp"
        class="mb-6 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_5%,transparent)] p-3 text-sm text-[var(--color-warning)]"
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

      <div class="card animate-slide-up p-6 shadow-[var(--shadow-md)]">
        <slot />
      </div>
    </section>
  </div>
</template>
