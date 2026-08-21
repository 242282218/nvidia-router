<script setup lang="ts">
import { computed } from 'vue'
import { Motion, useReducedMotion } from 'motion-v'

import UiIcon from '../../shared/ui/UiIcon.vue'
import { springSoft } from '../../shared/motion'

// 认证页共享骨架：极光氛围 + 网格纹理 + 玻璃拟态卡片。
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
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-10">
    <!-- Ambient layers：极光色斑缓慢漂移 + 透视网格，全部纯装饰 -->
    <div
      class="pointer-events-none fixed inset-0 overflow-hidden"
      aria-hidden="true"
    >
      <div class="auth-grid absolute inset-0" />
      <div class="auth-glow auth-glow-a absolute -top-48 right-[8%] h-[30rem] w-[30rem] rounded-full blur-3xl" />
      <div class="auth-glow auth-glow-b absolute -bottom-56 left-[4%] h-[34rem] w-[34rem] rounded-full blur-3xl" />
      <div class="auth-glow auth-glow-c absolute left-1/2 top-1/3 h-72 w-72 -translate-x-1/2 rounded-full blur-3xl" />
    </div>

    <Motion
      tag="section"
      class="relative w-full max-w-sm"
      :initial="reducedMotion ? { opacity: 0 } : { opacity: 0, y: 18, scale: 0.985 }"
      :animate="{ opacity: 1, y: 0, scale: 1 }"
      :transition="entrance"
    >
      <div class="mb-8 text-center">
        <div
          class="relative mx-auto flex h-14 w-14 items-center justify-center rounded-2xl text-lg font-bold"
          :class="badgeTone === 'brand'
            ? 'bg-[var(--color-brand)] text-[var(--color-brand-foreground)] shadow-[0_0_0_1px_rgba(118,185,0,0.35),0_8px_32px_rgba(118,185,0,0.28)]'
            : 'bg-[var(--color-warning)] text-[var(--color-canvas)] shadow-[0_0_0_1px_rgba(217,165,63,0.35),0_8px_32px_rgba(217,165,63,0.25)]'"
        >
          {{ badgeText }}
          <!-- 徽标呼吸光环：极淡、缓慢，暗示系统在线 -->
          <span
            v-if="badgeTone === 'brand'"
            class="absolute inset-0 -z-10 animate-ping rounded-2xl bg-[var(--color-brand)] opacity-20 [animation-duration:3s]"
            aria-hidden="true"
          />
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
        class="mb-6 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_5%,transparent)] p-3 text-sm text-[var(--color-warning)] backdrop-blur-sm"
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

      <!-- 玻璃拟态卡片：半透明表面 + 背景模糊 + 高光描边 -->
      <div class="relative rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-elevated)_82%,transparent)] p-6 shadow-[var(--shadow-overlay)] backdrop-blur-xl">
        <!-- 顶部高光发丝线：玻璃质感的点睛之笔 -->
        <div
          class="glass-highlight pointer-events-none absolute inset-x-6 top-0 h-px"
          aria-hidden="true"
        />
        <slot />
      </div>
    </Motion>
  </div>
</template>

<style scoped>
/* 顶部高光发丝线：亮色用暖白高光，暗色压暗避免刺眼 */
.glass-highlight {
  background: linear-gradient(to right, transparent, rgba(255, 255, 255, 0.75), transparent);
}

[data-theme='dark'] .glass-highlight {
  background: linear-gradient(to right, transparent, rgba(255, 255, 255, 0.16), transparent);
}

/* 透视网格：两层正交渐变线，径向遮罩让边缘淡出 */
.auth-grid {
  background-image:
    linear-gradient(to right, var(--color-border) 1px, transparent 1px),
    linear-gradient(to bottom, var(--color-border) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(ellipse 90% 70% at 50% 40%, black 30%, transparent 75%);
  opacity: 0.55;
}

/* 极光色斑：亮暗主题各一套配色；漂移动画在 reduced-motion 下全局降级 */
.auth-glow-a {
  background: radial-gradient(circle, rgba(217, 165, 63, 0.16), transparent 65%);
  animation: glow-drift 18s ease-in-out infinite alternate;
}

.auth-glow-b {
  background: radial-gradient(circle, rgba(118, 185, 0, 0.10), transparent 65%);
  animation: glow-drift 22s ease-in-out infinite alternate-reverse;
}

.auth-glow-c {
  background: radial-gradient(circle, rgba(61, 90, 160, 0.08), transparent 65%);
  animation: glow-drift 26s ease-in-out infinite alternate;
}

[data-theme='dark'] .auth-glow-a {
  background: radial-gradient(circle, rgba(217, 165, 63, 0.12), transparent 65%);
}

[data-theme='dark'] .auth-glow-b {
  background: radial-gradient(circle, rgba(118, 185, 0, 0.09), transparent 65%);
}

@keyframes glow-drift {
  from {
    transform: translate3d(0, 0, 0) scale(1);
  }
  to {
    transform: translate3d(-4%, 6%, 0) scale(1.08);
  }
}
</style>
