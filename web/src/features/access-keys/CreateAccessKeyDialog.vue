<script setup lang="ts">
import { ref, watch } from 'vue'

import { ApiError } from '../../shared/api/client'
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
  <Transition name="modal">
    <div
      v-if="open"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-access-key-title"
    >
      <section class="modal-panel max-w-lg">
        <!-- Created state -->
        <template v-if="plaintext">
          <div class="border-b border-[var(--color-border)] px-6 py-4">
            <h2
              id="create-access-key-title"
              class="text-base font-semibold text-[var(--color-text)]"
            >
              Access Key 已创建
            </h2>
          </div>
          <div class="p-6 space-y-4">
            <div class="rounded-lg border border-[#F59E0B]/30 bg-[#F59E0B]/5 p-3">
              <div class="flex items-start gap-2">
                <svg
                  class="mt-0.5 h-4 w-4 shrink-0 text-[#FBBF24]"
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
                <p class="text-sm text-[#FBBF24]">
                  明文仅显示这一次。关闭后无法恢复，请立即复制并安全保存。
                </p>
              </div>
            </div>
            <div class="rounded-lg bg-[var(--color-sunken)] border border-[var(--color-border)]">
              <code
                data-testid="created-access-key"
                class="block break-all p-4 font-mono text-sm text-[var(--color-info)]"
              >{{ plaintext }}</code>
            </div>
            <Transition name="fade">
              <p
                v-if="copyMessage"
                class="text-sm text-[var(--color-text-secondary)] text-center"
              >
                {{ copyMessage }}
              </p>
            </Transition>
            <div class="flex justify-end gap-3">
              <button
                data-testid="copy-created-access-key"
                class="btn-secondary rounded-lg px-4 py-2 text-sm"
                type="button"
                @click="copyKey"
              >
                <span class="flex items-center gap-2">
                  <svg
                    class="h-4 w-4"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"
                    />
                  </svg>
                  复制
                </span>
              </button>
              <button
                data-testid="close-created-access-key"
                class="btn-primary rounded-lg px-4 py-2 text-sm"
                type="button"
                @click="close"
              >
                我已保存，关闭
              </button>
            </div>
          </div>
        </template>

        <!-- Create form -->
        <template v-else>
          <div class="border-b border-[var(--color-border)] px-6 py-4">
            <h2
              id="create-access-key-title"
              class="text-base font-semibold text-[var(--color-text)]"
            >
              创建 Access Key
            </h2>
          </div>
          <div class="p-6">
            <form
              data-testid="create-access-key-form"
              class="space-y-4"
              @submit.prevent="createKey"
            >
              <label class="block">
                <span class="text-sm font-medium text-[var(--color-text-secondary)]">设备或客户端名称</span>
                <input
                  id="access-key-name"
                  v-model="name"
                  data-testid="access-key-name"
                  class="input-field mt-1.5"
                  autocomplete="off"
                  maxlength="120"
                >
              </label>
              <Transition name="slide">
                <p
                  v-if="errorMessage"
                  class="text-sm text-[#F87171]"
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
                  class="btn-primary rounded-lg px-4 py-2 text-sm"
                  type="submit"
                  :disabled="submitting"
                >
                  {{ submitting ? '创建中…' : '创建' }}
                </button>
              </div>
            </form>
          </div>
        </template>
      </section>
    </div>
  </Transition>
</template>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: all 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from section,
.modal-leave-to section {
  transform: scale(0.95);
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
.slide-enter-active,
.slide-leave-active {
  transition: all 0.2s ease;
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>