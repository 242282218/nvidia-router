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
  input.select()
  globalThis.document.execCommand('copy')
  input.remove()
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
  <div
    v-if="open"
    class="fixed inset-0 z-20 grid place-items-center bg-slate-950/80 p-4"
    role="dialog"
    aria-modal="true"
    aria-labelledby="create-access-key-title"
  >
    <section class="w-full max-w-lg rounded-xl border border-slate-700 bg-slate-900 p-5 shadow-2xl">
      <template v-if="plaintext">
        <h2
          id="create-access-key-title"
          class="text-lg font-semibold"
        >
          Access Key 已创建
        </h2>
        <p class="mt-2 text-sm text-amber-300">
          明文仅显示这一次。关闭后无法恢复，请立即复制并安全保存。
        </p>
        <code
          data-testid="created-access-key"
          class="mt-4 block break-all rounded-lg bg-slate-950 p-4 text-sm text-indigo-200"
        >{{ plaintext }}</code>
        <p
          v-if="copyMessage"
          class="mt-2 text-sm text-slate-300"
        >
          {{ copyMessage }}
        </p>
        <div class="mt-5 flex justify-end gap-3">
          <button
            data-testid="copy-created-access-key"
            class="rounded-lg border border-indigo-400 px-4 py-2 text-sm text-indigo-200"
            type="button"
            @click="copyKey"
          >
            复制
          </button>
          <button
            data-testid="close-created-access-key"
            class="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white"
            type="button"
            @click="close"
          >
            我已保存，关闭
          </button>
        </div>
      </template>
      <template v-else>
        <h2
          id="create-access-key-title"
          class="text-lg font-semibold"
        >
          创建 Access Key
        </h2>
        <form
          data-testid="create-access-key-form"
          class="mt-4"
          @submit.prevent="createKey"
        >
          <label
            class="text-sm text-slate-300"
            for="access-key-name"
          >设备或客户端名称</label>
          <input
            id="access-key-name"
            v-model="name"
            data-testid="access-key-name"
            class="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm"
            autocomplete="off"
            maxlength="120"
          >
          <p
            v-if="errorMessage"
            class="mt-2 text-sm text-rose-300"
            role="alert"
          >
            {{ errorMessage }}
          </p>
          <div class="mt-5 flex justify-end gap-3">
            <button
              class="rounded-lg border border-slate-700 px-4 py-2 text-sm"
              type="button"
              @click="close"
            >
              取消
            </button>
            <button
              class="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
              type="submit"
              :disabled="submitting"
            >
              创建
            </button>
          </div>
        </form>
      </template>
    </section>
  </div>
</template>
