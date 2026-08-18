<script setup lang="ts">
import { reactive, ref, watch } from 'vue'

import { ApiError } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import { accessKeysApi } from './api'
import type { AccessKey, AccessKeyPolicy } from './types'

const props = defineProps<{ open: boolean; accessKey: AccessKey | null }>()
const emit = defineEmits<{
  close: []
  saved: []
}>()

const rpm = ref('')
const tpm = ref('')
const maxConcurrent = ref('')
const tokenBudget = ref('')
const expiresAt = ref('')
const errorMessage = ref('')
const saving = ref(false)
const fieldErrors = reactive({ rpm: '', tpm: '', maxConcurrent: '', tokenBudget: '', expiresAt: '' })

watch(() => props.open, (open) => {
  if (!open) return
  const policy = props.accessKey
  if (!policy) return
  rpm.value = String(policy.rpm_limit)
  tpm.value = String(policy.tpm_limit)
  maxConcurrent.value = String(policy.max_concurrent)
  tokenBudget.value = policy.token_budget > 0 ? String(policy.token_budget) : ''
  expiresAt.value = policy.expires_at ? toLocalInput(policy.expires_at) : ''
  errorMessage.value = ''
  resetFieldErrors()
})

async function save(): Promise<void> {
  if (!props.accessKey) return
  const policy = buildPolicy()
  if (!policy) return
  saving.value = true
  errorMessage.value = ''
  try {
    await accessKeysApi.updatePolicy(props.accessKey.id, policy)
    emit('saved')
    emit('close')
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : 'Access Key 策略保存失败。'
  } finally {
    saving.value = false
  }
}

function buildPolicy(): AccessKeyPolicy | null {
  resetFieldErrors()
  const rpmValue = parseLimit(rpm.value)
  const tpmValue = parseLimit(tpm.value)
  const maxConcurrentValue = parseLimit(maxConcurrent.value)
  const tokenBudgetValue = parseTokenBudget(tokenBudget.value)
  if (!Number.isInteger(rpmValue) || rpmValue < 0 || rpmValue > 100_000) {
    fieldErrors.rpm = '请输入 0-100000 的整数，0 表示不限制。'
  }
  if (!Number.isInteger(tpmValue) || tpmValue < 0 || tpmValue > 1_000_000_000) {
    fieldErrors.tpm = '请输入 0-1000000000 的整数，0 表示不限制。'
  }
  if (!Number.isInteger(maxConcurrentValue) || maxConcurrentValue < 0 || maxConcurrentValue > 10_000) {
    fieldErrors.maxConcurrent = '请输入 0-10000 的整数，0 表示不限制。'
  }
  if (tokenBudgetValue === null) {
    fieldErrors.tokenBudget = '请输入 0-1000000000000 的整数，0 表示不限制。'
  }
  let expiresAtValue: string | null = null
  if (expiresAt.value.trim()) {
    const parsed = new Date(expiresAt.value)
    if (Number.isNaN(parsed.getTime()) || parsed.getTime() <= Date.now()) {
      fieldErrors.expiresAt = '过期时间必须是未来的日期时间。'
    } else {
      expiresAtValue = parsed.toISOString()
    }
  }
  if (Object.values(fieldErrors).some(Boolean)) return null
  return {
    expires_at: expiresAtValue,
    rpm_limit: rpmValue,
    tpm_limit: tpmValue,
    max_concurrent: maxConcurrentValue,
    token_budget: tokenBudgetValue as number,
  }
}

function parseLimit(raw: string): number {
  if (raw.trim() === '') return Number.NaN
  return Number(raw)
}

// An empty token budget maps to 0 (unlimited) by default; a non-empty value
// must be a non-negative integer within the allowed cap.
function parseTokenBudget(raw: string): number | null {
  if (raw.trim() === '') return 0
  const value = Number(raw)
  if (!Number.isInteger(value) || value < 0 || value > 1_000_000_000_000) return null
  return value
}

function toLocalInput(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function resetFieldErrors(): void {
  fieldErrors.rpm = ''
  fieldErrors.tpm = ''
  fieldErrors.maxConcurrent = ''
  fieldErrors.tokenBudget = ''
  fieldErrors.expiresAt = ''
}

function close(): void {
  emit('close')
}
</script>

<template>
  <UiModal
    :open="open"
    :title="`编辑策略 · ${accessKey?.name ?? ''}`"
    size="sm"
    @close="close"
  >
    <form
      data-testid="edit-access-key-policy-form"
      class="space-y-4"
      novalidate
      @submit.prevent="save"
    >
      <div>
        <label
          class="field-label"
          for="policy-rpm"
        >RPM 限制</label>
        <input
          id="policy-rpm"
          :value="rpm"
          data-testid="access-key-rpm-limit"
          class="input-field"
          type="number"
          min="0"
          max="100000"
          step="1"
          :aria-invalid="Boolean(fieldErrors.rpm)"
          @input="(e: Event) => { rpm = (e.target as HTMLInputElement).value }"
        >
        <span
          v-if="fieldErrors.rpm"
          data-testid="access-key-rpm-error"
          class="mt-1.5 block text-xs text-[var(--color-danger)]"
          role="alert"
        >{{ fieldErrors.rpm }}</span>
        <span class="mt-1.5 block text-xs text-[var(--color-text-muted)]">每分钟请求数上限，0 表示不限制。</span>
      </div>

      <div>
        <label
          class="field-label"
          for="policy-tpm"
        >TPM 限制</label>
        <input
          id="policy-tpm"
          :value="tpm"
          data-testid="access-key-tpm-limit"
          class="input-field"
          type="number"
          min="0"
          max="1000000000"
          step="1"
          :aria-invalid="Boolean(fieldErrors.tpm)"
          @input="(e: Event) => { tpm = (e.target as HTMLInputElement).value }"
        >
        <span
          v-if="fieldErrors.tpm"
          data-testid="access-key-tpm-error"
          class="mt-1.5 block text-xs text-[var(--color-danger)]"
          role="alert"
        >{{ fieldErrors.tpm }}</span>
        <span class="mt-1.5 block text-xs text-[var(--color-text-muted)]">每分钟 Token 数上限，0 表示不限制。</span>
      </div>

      <div>
        <label
          class="field-label"
          for="policy-max-concurrent"
        >最大并发</label>
        <input
          id="policy-max-concurrent"
          :value="maxConcurrent"
          data-testid="access-key-max-concurrent"
          class="input-field"
          type="number"
          min="0"
          max="10000"
          step="1"
          :aria-invalid="Boolean(fieldErrors.maxConcurrent)"
          @input="(e: Event) => { maxConcurrent = (e.target as HTMLInputElement).value }"
        >
        <span
          v-if="fieldErrors.maxConcurrent"
          data-testid="access-key-max-concurrent-error"
          class="mt-1.5 block text-xs text-[var(--color-danger)]"
          role="alert"
        >{{ fieldErrors.maxConcurrent }}</span>
        <span class="mt-1.5 block text-xs text-[var(--color-text-muted)]">同时进行的请求数上限，0 表示不限制。</span>
      </div>

      <div>
        <label
          class="field-label"
          for="policy-token-budget"
        >Token 总预算</label>
        <input
          id="policy-token-budget"
          :value="tokenBudget"
          data-testid="access-key-token-budget"
          class="input-field"
          type="number"
          min="0"
          max="1000000000000"
          step="1"
          :aria-invalid="Boolean(fieldErrors.tokenBudget)"
          @input="(e: Event) => { tokenBudget = (e.target as HTMLInputElement).value }"
        >
        <span
          v-if="fieldErrors.tokenBudget"
          data-testid="access-key-token-budget-error"
          class="mt-1.5 block text-xs text-[var(--color-danger)]"
          role="alert"
        >{{ fieldErrors.tokenBudget }}</span>
        <span class="mt-1.5 block text-xs text-[var(--color-text-muted)]">该 Key 累计可消耗的 Token 上限，用尽后拒绝请求，0 表示不限制。</span>
      </div>

      <div>
        <label
          class="field-label"
          for="policy-expires-at"
        >过期时间</label>
        <input
          id="policy-expires-at"
          v-model="expiresAt"
          data-testid="access-key-expires-at"
          class="input-field"
          type="datetime-local"
          :aria-invalid="Boolean(fieldErrors.expiresAt)"
        >
        <span
          v-if="fieldErrors.expiresAt"
          data-testid="access-key-expires-at-error"
          class="mt-1.5 block text-xs text-[var(--color-danger)]"
          role="alert"
        >{{ fieldErrors.expiresAt }}</span>
        <span class="mt-1.5 block text-xs text-[var(--color-text-muted)]">留空表示永不过期。</span>
      </div>

      <p
        v-if="errorMessage"
        data-testid="edit-access-key-policy-error"
        class="text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ errorMessage }}
      </p>

      <div class="flex justify-end gap-2">
        <UiButton
          variant="ghost"
          @click="close"
        >
          取消
        </UiButton>
        <UiButton
          data-testid="save-access-key-policy"
          variant="primary"
          type="submit"
          :loading="saving"
          loading-label="保存中…"
        >
          保存策略
        </UiButton>
      </div>
    </form>
  </UiModal>
</template>
