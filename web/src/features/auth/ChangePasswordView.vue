<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { ApiError } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiField from '../../shared/ui/UiField.vue'
import AuthLayout from './AuthLayout.vue'
import { useSession } from './useSession'

const router = useRouter()
const session = useSession()
const currentPassword = ref('')
const newPassword = ref('')
const formError = ref('')
const submitting = ref(false)

// 实时强度提示：把「至少 12 字符」的校验从提交后报错前移到输入中可见。
const lengthHint = computed(() => {
  const length = Array.from(newPassword.value).length
  if (length === 0) return ''
  return length < 12 ? `还需 ${12 - length} 个字符` : '长度符合要求'
})

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
  <AuthLayout
    title="修改管理员密码"
    subtitle="首次登录需要修改密码，完成后进入管理端"
    badge-tone="warning"
    badge-text="!"
  >
    <form
      class="space-y-4"
      @submit.prevent="submit"
    >
      <UiField
        label="当前密码"
        input-id="current-password"
      >
        <input
          id="current-password"
          v-model="currentPassword"
          autocomplete="current-password"
          class="input-field"
          name="current-password"
          required
          type="password"
        >
      </UiField>

      <UiField
        label="新密码"
        input-id="new-password"
        :hint="lengthHint || '至少 12 个字符，且不能为 admin。'"
        :error="''"
      >
        <input
          id="new-password"
          v-model="newPassword"
          autocomplete="new-password"
          class="input-field"
          minlength="12"
          name="new-password"
          required
          type="password"
        >
      </UiField>

      <p
        v-if="formError"
        class="rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-danger)_20%,transparent)] bg-[color-mix(in_srgb,var(--color-danger)_5%,transparent)] px-3 py-2 text-sm text-[var(--color-danger)]"
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
        loading-label="修改中…"
      >
        修改密码
      </UiButton>
    </form>
  </AuthLayout>
</template>
