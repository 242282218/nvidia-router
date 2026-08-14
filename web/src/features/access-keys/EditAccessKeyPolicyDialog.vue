<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'

import { ApiError } from '../../shared/api/client'
import { useDialog } from '../../shared/useDialog'
import { accessKeysApi } from './api'
import type { AccessKey, AccessKeyPolicy } from './types'

const props = defineProps<{ open: boolean; accessKey: AccessKey | null }>()
const emit = defineEmits<{
  close: []
  saved: []
}>()

const panel = ref<globalThis.HTMLElement | null>(null)
useDialog(computed(() => props.open), panel, () => emit('close'))

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
  <Transition name="modal">
    <div
      v-if="open"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      aria-labelledby="edit-access-key-policy-title"
    >
      <section
        ref="panel"
        class="modal-panel max-w-lg"
      >
        <div class="border-b border-[var(--color-border)] px-6 py-4">
          <h2
            id="edit-access-key-policy-title"
            class="text-base font-semibold text-[var(--color-text)]"
          >
            编辑策略 · {{ accessKey?.name }}
          </h2>
        </div>
        <div class="p-6">
          <form
            data-testid="edit-access-key-policy-form"
            class="space-y-4"
            novalidate
            @submit.prevent="save"
          >
            <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
              <span>RPM 限制</span>
              <input
                :value="rpm"
                data-testid="access-key-rpm-limit"
                class="input-field mt-1.5"
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
                class="mt-1 block text-xs text-[var(--color-danger)]"
                role="alert"
              >{{ fieldErrors.rpm }}</span>
              <span class="mt-1 block text-xs text-[var(--color-text-muted)]">每分钟请求数上限，0 表示不限制。</span>
            </label>

            <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
              <span>TPM 限制</span>
              <input
                :value="tpm"
                data-testid="access-key-tpm-limit"
                class="input-field mt-1.5"
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
                class="mt-1 block text-xs text-[var(--color-danger)]"
                role="alert"
              >{{ fieldErrors.tpm }}</span>
              <span class="mt-1 block text-xs text-[var(--color-text-muted)]">每分钟 Token 数上限，0 表示不限制。</span>
            </label>

            <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
              <span>最大并发</span>
              <input
                :value="maxConcurrent"
                data-testid="access-key-max-concurrent"
                class="input-field mt-1.5"
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
                class="mt-1 block text-xs text-[var(--color-danger)]"
                role="alert"
              >{{ fieldErrors.maxConcurrent }}</span>
              <span class="mt-1 block text-xs text-[var(--color-text-muted)]">同时进行的请求数上限，0 表示不限制。</span>
            </label>

            <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
              <span>Token 总预算</span>
              <input
                :value="tokenBudget"
                data-testid="access-key-token-budget"
                class="input-field mt-1.5"
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
                class="mt-1 block text-xs text-[var(--color-danger)]"
                role="alert"
              >{{ fieldErrors.tokenBudget }}</span>
              <span class="mt-1 block text-xs text-[var(--color-text-muted)]">该 Key 累计可消耗的 Token 上限，用尽后拒绝请求，0 表示不限制。</span>
            </label>

            <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
              <span>过期时间</span>
              <input
                v-model="expiresAt"
                data-testid="access-key-expires-at"
                class="input-field mt-1.5"
                type="datetime-local"
                :aria-invalid="Boolean(fieldErrors.expiresAt)"
              >
              <span
                v-if="fieldErrors.expiresAt"
                data-testid="access-key-expires-at-error"
                class="mt-1 block text-xs text-[var(--color-danger)]"
                role="alert"
              >{{ fieldErrors.expiresAt }}</span>
              <span class="mt-1 block text-xs text-[var(--color-text-muted)]">留空表示永不过期。</span>
            </label>

            <Transition name="slide">
              <p
                v-if="errorMessage"
                data-testid="edit-access-key-policy-error"
                class="text-sm text-[var(--color-danger)]"
                role="alert"
              >
                {{ errorMessage }}
              </p>
            </Transition>

            <div class="flex justify-end gap-3">
              <button
                class="btn-secondary rounded-lg px-4 py-2 text-sm"
                type="button"
                @click="close"
              >
                取消
              </button>
              <button
                data-testid="save-access-key-policy"
                class="btn-primary rounded-lg px-4 py-2 text-sm"
                type="submit"
                :disabled="saving"
              >
                {{ saving ? '保存中…' : '保存策略' }}
              </button>
            </div>
          </form>
        </div>
      </section>
    </div>
  </Transition>
</template>

<style scoped>
.modal-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.modal-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from section,
.modal-leave-to section {
  transform: scale(0.95);
}
.slide-enter-active {
  transition: opacity 0.2s cubic-bezier(0.0, 0.0, 0.2, 1), transform 0.2s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.slide-leave-active {
  transition: opacity 0.14s cubic-bezier(0.4, 0.0, 1, 1), transform 0.14s cubic-bezier(0.4, 0.0, 1, 1);
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
