<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { ApiError } from '../../shared/api/client'
import { useSession } from './useSession'

const router = useRouter()
const session = useSession()
const currentPassword = ref('')
const newPassword = ref('')
const formError = ref('')
const submitting = ref(false)
const isPlainHttp = computed(() => globalThis.location.protocol === 'http:')

async function submit(): Promise<void> {
  formError.value = validateNewPassword(newPassword.value)
  if (formError.value) {
    return
  }

  submitting.value = true
  try {
    await session.changePassword(currentPassword.value, newPassword.value)
    await router.push('/')
  } catch (error) {
    formError.value =
      error instanceof ApiError ? error.message : '修改密码失败，请检查网络连接后重试。'
  } finally {
    currentPassword.value = ''
    newPassword.value = ''
    submitting.value = false
  }
}

function validateNewPassword(password: string): string {
  if (password === 'admin') {
    return '新密码不能为 admin。'
  }
  if (Array.from(password).length < 12) {
    return '新密码至少需要 12 个字符。'
  }
  return ''
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
        当前页面使用 HTTP，密码会通过明文连接传输。请仅在可信网络中操作。
      </div>

      <h1 class="text-2xl font-semibold">
        修改管理员密码
      </h1>
      <p class="mt-2 text-sm text-slate-400">
        新密码至少 12 个字符，且不能为 admin。完成修改后才能进入管理端。
      </p>

      <form
        class="mt-6 space-y-4"
        @submit.prevent="submit"
      >
        <label class="block">
          <span class="text-sm text-slate-300">当前密码</span>
          <input
            v-model="currentPassword"
            autocomplete="current-password"
            class="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
            name="current-password"
            required
            type="password"
          >
        </label>

        <label class="block">
          <span class="text-sm text-slate-300">新密码</span>
          <input
            v-model="newPassword"
            autocomplete="new-password"
            class="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2"
            minlength="12"
            name="new-password"
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
          {{ submitting ? '修改中…' : '修改密码' }}
        </button>
      </form>
    </section>
  </main>
</template>

