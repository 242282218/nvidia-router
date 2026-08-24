<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

import { ApiError } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiField from '../../shared/ui/UiField.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import AuthLayout from './AuthLayout.vue'
import { useSession } from './useSession'

const router = useRouter()
const session = useSession()
const username = ref('admin')
const password = ref('')
const showPassword = ref(false)
const formError = ref('')
const submitting = ref(false)
// 每次失败自增，作为 :key 重触发抖动动画
const errorTick = ref(0)

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
    errorTick.value += 1
  } finally {
    password.value = ''
    submitting.value = false
  }
}
</script>

<template>
  <AuthLayout
    title="管理员登录"
    subtitle="NVIDIA Router 管理控制台"
  >
    <template #brand>
      <p
        class="sr-only"
        data-testid="login-brand"
      >
        NVIDIA API Router
      </p>
    </template>

    <form
      class="space-y-4"
      @submit.prevent="submit"
    >
      <UiField
        label="用户名"
        input-id="login-username"
      >
        <input
          id="login-username"
          v-model="username"
          autocomplete="username"
          class="input-field"
          name="username"
          required
        >
      </UiField>

      <UiField
        label="密码"
        input-id="login-password"
      >
        <div class="relative">
          <input
            id="login-password"
            v-model="password"
            autocomplete="current-password"
            class="input-field pr-10"
            name="password"
            required
            :type="showPassword ? 'text' : 'password'"
          >
          <button
            class="absolute right-1.5 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-[var(--radius-control)] text-[var(--color-text-subtle)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] pointer-coarse:h-11 pointer-coarse:w-11"
            type="button"
            :aria-label="showPassword ? '隐藏密码' : '显示密码'"
            :aria-pressed="showPassword"
            @click="showPassword = !showPassword"
          >
            <UiIcon
              :name="showPassword ? 'eye-off' : 'eye'"
              :size="15"
            />
          </button>
        </div>
      </UiField>

      <p
        v-if="formError"
        :key="errorTick"
        class="animate-shake rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-danger)_20%,transparent)] bg-[color-mix(in_srgb,var(--color-danger)_5%,transparent)] px-3 py-2 text-sm text-[var(--color-danger)]"
        data-testid="form-error"
        role="alert"
      >
        {{ formError }}
      </p>

      <UiButton
        variant="primary"
        type="submit"
        block
        :loading="submitting"
        loading-label="登录中…"
      >
        登录
      </UiButton>
    </form>
  </AuthLayout>
</template>
