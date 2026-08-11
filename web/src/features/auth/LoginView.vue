<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { ApiError } from '../../shared/api/client'
import { useSession } from './useSession'

const router = useRouter()
const session = useSession()
const username = ref('admin')
const password = ref('')
const formError = ref('')
const submitting = ref(false)
const isPlainHttp = computed(() => globalThis.location.protocol === 'http:')

async function submit(): Promise<void> {
  formError.value = ''
  submitting.value = true
  try {
    await session.login(username.value, password.value)
    const state = session.state.value
    await router.push(
      state.kind === 'authenticated' && state.mustChangePassword ? '/change-password' : '/',
    )
  } catch (error) {
    formError.value =
      error instanceof ApiError ? error.message : '登录失败，请检查网络连接后重试。'
  } finally {
    password.value = ''
    submitting.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center bg-[var(--color-canvas)] px-4">
    <!-- Ambient decoration -->
    <div class="fixed inset-0 overflow-hidden pointer-events-none">
      <div class="absolute -top-40 -right-40 h-80 w-80 rounded-full bg-[var(--color-accent)]/5 blur-3xl" />
      <div class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-[var(--color-info)]/5 blur-3xl" />
    </div>

    <section class="relative w-full max-w-sm animate-fade-in">
      <!-- Logo -->
      <div class="mb-8 text-center">
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-[var(--color-accent)] text-lg font-bold text-[var(--color-canvas)]">
          N
        </div>
        <h1 class="mt-4 text-lg font-semibold text-[var(--color-text)]">
          管理员登录
        </h1>
        <p
          class="sr-only"
          data-testid="login-brand"
        >
          NVIDIA API Router
        </p>
        <p class="mt-1 text-sm text-[var(--color-text-muted)]">
          管理控制台
        </p>
      </div>

      <!-- HTTP warning -->
      <div
        v-if="isPlainHttp"
        class="mb-6 rounded-lg border border-[#F59E0B]/30 bg-[#F59E0B]/5 p-3 text-sm text-[#FBBF24]"
        role="alert"
      >
        <div class="flex items-start gap-2">
          <svg
            class="mt-0.5 h-4 w-4 shrink-0"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"
            />
          </svg>
          <span>当前页面使用 HTTP，账号和密码会通过明文连接传输。请仅在可信网络中操作。</span>
        </div>
      </div>

      <!-- Login card -->
      <div class="card p-6 animate-slide-up">
        <h2 class="text-base font-medium text-[var(--color-text)]">
          管理员登录
        </h2>
        <p class="mt-1 text-sm text-[var(--color-text-muted)]">
          请输入凭据继续
        </p>

        <form
          class="mt-6 space-y-4"
          @submit.prevent="submit"
        >
          <label class="block">
            <span class="text-sm font-medium text-[var(--color-text-secondary)]">用户名</span>
            <input
              v-model="username"
              autocomplete="username"
              class="input-field mt-1.5"
              name="username"
              required
            >
          </label>

          <label class="block">
            <span class="text-sm font-medium text-[var(--color-text-secondary)]">密码</span>
            <input
              v-model="password"
              autocomplete="current-password"
              class="input-field mt-1.5"
              name="password"
              required
              type="password"
            >
          </label>

          <Transition name="slide">
            <p
              v-if="formError"
              class="rounded-lg bg-[#EF4444]/5 border border-[#EF4444]/20 px-3 py-2 text-sm text-[#F87171]"
              data-testid="form-error"
              role="status"
            >
              {{ formError }}
            </p>
          </Transition>

          <button
            class="btn-primary w-full rounded-lg px-4 py-2.5 text-sm disabled:opacity-40"
            :disabled="submitting"
            type="submit"
          >
            <span class="flex items-center justify-center gap-2">
              <svg
                v-if="submitting"
                class="h-4 w-4 animate-spin"
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle
                  class="opacity-25"
                  cx="12"
                  cy="12"
                  r="10"
                  stroke="currentColor"
                  stroke-width="4"
                />
                <path
                  class="opacity-75"
                  fill="currentColor"
                  d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                />
              </svg>
              {{ submitting ? '登录中…' : '登录' }}
            </span>
          </button>
        </form>
      </div>
    </section>
  </div>
</template>

<style scoped>
.slide-enter-active {
  transition: all 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.slide-leave-active {
  transition: all 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>