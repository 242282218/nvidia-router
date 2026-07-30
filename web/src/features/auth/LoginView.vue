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
  <main class="min-h-screen bg-slate-950 px-4 py-12 text-slate-100">
    <section class="mx-auto max-w-md rounded-xl bg-slate-900 p-6 shadow-xl">
      <div
        v-if="isPlainHttp"
        class="mb-5 rounded-lg border border-amber-500/60 bg-amber-950/60 p-3 text-sm text-amber-100"
        role="alert"
      >
        当前页面使用 HTTP，账号和密码会通过明文连接传输。请仅在可信网络中操作。
      </div>

      <h1 class="text-2xl font-semibold">
        管理员登录
      </h1>
      <p class="mt-2 text-sm text-slate-400">
        登录 NVIDIA API Router 管理端。
      </p>

      <form
        class="mt-6 space-y-4"
        @submit.prevent="submit"
      >
        <label class="block">
          <span class="text-sm text-slate-300">用户名</span>
          <input
            v-model="username"
            autocomplete="username"
            class="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
            name="username"
            required
          >
        </label>

        <label class="block">
          <span class="text-sm text-slate-300">密码</span>
          <input
            v-model="password"
            autocomplete="current-password"
            class="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
            name="password"
            required
            type="password"
          >
        </label>

        <p
          v-if="formError"
          class="text-sm text-red-300"
          data-testid="form-error"
          role="status"
        >
          {{ formError }}
        </p>

        <button
          class="w-full rounded-lg bg-green-500 px-4 py-2 font-medium text-slate-950 disabled:opacity-50"
          :disabled="submitting"
          type="submit"
        >
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </form>
    </section>
  </main>
</template>

