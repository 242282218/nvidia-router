<script setup lang="ts">
import { ref, watch } from 'vue'

import { ApiError } from '../../shared/api/client'
import UiButton from '../../shared/ui/UiButton.vue'
import UiField from '../../shared/ui/UiField.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import UiModal from '../../shared/ui/UiModal.vue'
import { accessKeysApi } from './api'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  close: []
  created: []
}>()

const name = ref('')
const plaintext = ref('')
const errorMessage = ref('')
const copyMessage = ref('')
const submitting = ref(false)

watch(() => props.open, (open) => {
  if (!open) clearSensitiveState()
})

async function createKey(): Promise<void> {
  const trimmedName = name.value.trim()
  errorMessage.value = ''
  if (!trimmedName) {
    errorMessage.value = '请输入设备或客户端名称。'
    return
  }
  submitting.value = true
  try {
    const created = await accessKeysApi.create(trimmedName)
    plaintext.value = created.key
    name.value = ''
    emit('created')
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : 'Access Key 创建失败。'
  } finally {
    submitting.value = false
  }
}

async function copyKey(): Promise<void> {
  if (!plaintext.value) return
  try {
    if (globalThis.navigator.clipboard) {
      await globalThis.navigator.clipboard.writeText(plaintext.value)
    } else {
      legacyCopy(plaintext.value)
    }
    copyMessage.value = '已复制。'
  } catch {
    copyMessage.value = '复制失败，请手动复制。'
  }
}

function legacyCopy(value: string): void {
  const input = globalThis.document.createElement('textarea')
  input.value = value
  input.style.position = 'fixed'
  input.style.opacity = '0'
  globalThis.document.body.append(input)
  try {
    input.select()
    const copied = globalThis.document.execCommand('copy')
    if (!copied) throw new Error('legacy copy failed')
  } finally {
    input.value = ''
    input.remove()
  }
}

function close(): void {
  clearSensitiveState()
  emit('close')
}

function clearSensitiveState(): void {
  name.value = ''
  plaintext.value = ''
  errorMessage.value = ''
  copyMessage.value = ''
}
</script>

<template>
  <UiModal
    :open="open"
    :title="plaintext ? 'Access Key 已创建' : '创建 Access Key'"
    size="sm"
    @close="close"
  >
    <!-- Created state -->
    <div
      v-if="plaintext"
      class="space-y-4"
    >
      <div class="flex items-start gap-2.5 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_5%,transparent)] p-3">
        <UiIcon
          name="warning"
          :size="16"
          class="mt-0.5 shrink-0 text-[var(--color-warning)]"
        />
        <p class="text-sm text-[var(--color-warning)]">
          明文仅显示这一次。关闭后无法恢复，请立即复制并安全保存。
        </p>
      </div>
      <div class="panel-inset border border-[var(--color-border)]">
        <code
          data-testid="created-access-key"
          class="block break-all p-4 font-mono-data text-sm text-[var(--color-info)]"
        >{{ plaintext }}</code>
      </div>
      <p
        v-if="copyMessage"
        class="text-center text-sm text-[var(--color-text-secondary)]"
      >
        {{ copyMessage }}
      </p>
      <div class="flex justify-end gap-2">
        <UiButton
          data-testid="copy-created-access-key"
          variant="secondary"
          icon="copy"
          @click="copyKey"
        >
          复制
        </UiButton>
        <UiButton
          data-testid="close-created-access-key"
          variant="primary"
          @click="close"
        >
          我已保存，关闭
        </UiButton>
      </div>
    </div>

    <!-- Create form -->
    <form
      v-else
      data-testid="create-access-key-form"
      class="space-y-4"
      @submit.prevent="createKey"
    >
      <UiField
        label="设备或客户端名称"
        input-id="access-key-name"
        :error="errorMessage"
        hint="用于区分调用来源，例如「家庭电脑」「CI 流水线」。"
      >
        <input
          id="access-key-name"
          v-model="name"
          data-testid="access-key-name"
          class="input-field"
          autocomplete="off"
          maxlength="120"
        >
      </UiField>
      <div class="flex justify-end gap-2">
        <UiButton
          variant="ghost"
          @click="close"
        >
          取消
        </UiButton>
        <UiButton
          variant="primary"
          type="submit"
          :loading="submitting"
          loading-label="创建中…"
        >
          创建
        </UiButton>
      </div>
    </form>
  </UiModal>
</template>
