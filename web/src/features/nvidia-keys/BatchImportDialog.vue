<script setup lang="ts">
import { ref } from 'vue'

import { ApiError } from '../../shared/api/client'
import { nvidiaKeysApi } from './api'
import type { ImportResult } from './types'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; imported: [] }>()
const text = ref('')
const results = ref<ImportResult[]>([])
const errorMessage = ref('')
const submitting = ref(false)

async function submit(): Promise<void> {
  const keys = text.value
  text.value = ''
  results.value = []
  errorMessage.value = ''
  if (!keys.trim()) {
    errorMessage.value = '请先输入至少一行 NVIDIA Key。'
    return
  }
  submitting.value = true
  try {
    results.value = (await nvidiaKeysApi.importBatch(keys)).data
    emit('imported')
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '批量导入失败，请稍后重试。'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-20 flex items-center justify-center bg-black/70 p-4"
    role="dialog"
    aria-modal="true"
  >
    <section class="w-full max-w-2xl rounded-xl border border-slate-700 bg-slate-900 p-5 shadow-2xl">
      <div class="flex items-center justify-between gap-4">
        <h2 class="text-lg font-semibold">
          批量导入 NVIDIA Key
        </h2>
        <button
          class="text-slate-400 hover:text-white"
          type="button"
          aria-label="关闭"
          @click="emit('close')"
        >
          关闭
        </button>
      </div>
      <p class="mt-2 text-sm text-slate-400">
        每行一个 Key。提交后输入会立即清空，页面不会保留完整 Key。
      </p>
      <form
        class="mt-4 space-y-3"
        @submit.prevent="submit"
      >
        <textarea
          v-model="text"
          class="min-h-36 w-full rounded-lg border border-slate-700 bg-slate-950 p-3 font-mono text-sm"
          name="batch-keys"
          autocomplete="off"
          spellcheck="false"
          placeholder="每行一个 NVIDIA Key"
        />
        <p
          v-if="errorMessage"
          class="text-sm text-rose-300"
          role="alert"
        >
          {{ errorMessage }}
        </p>
        <div class="flex justify-end gap-2">
          <button
            class="rounded-lg border border-slate-700 px-3 py-2 text-sm"
            type="button"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            class="rounded-lg bg-indigo-500 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
            type="submit"
            :disabled="submitting"
          >
            导入
          </button>
        </div>
      </form>
      <div
        v-if="results.length"
        class="mt-5 overflow-x-auto rounded-lg border border-slate-800"
      >
        <table class="min-w-full text-left text-sm">
          <thead class="border-b border-slate-800 text-slate-400">
            <tr>
              <th class="px-3 py-2">
                行
              </th><th class="px-3 py-2">
                Key
              </th><th class="px-3 py-2">
                状态
              </th><th class="px-3 py-2">
                原因
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="result in results"
              :key="`${result.line}-${result.masked}`"
              class="border-b border-slate-800/60 last:border-0"
            >
              <td class="px-3 py-2">
                {{ result.line ?? '—' }}
              </td>
              <td class="px-3 py-2 font-mono">
                {{ result.masked || '—' }}
              </td>
              <td class="px-3 py-2">
                {{ result.status }}
              </td>
              <td class="px-3 py-2 text-slate-400">
                {{ result.reason || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
