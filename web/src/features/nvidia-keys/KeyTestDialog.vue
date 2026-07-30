<script setup lang="ts">
import type { KeyTestResult } from './types'

defineProps<{ open: boolean; results: KeyTestResult[] }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <div
    v-if="open && results.length"
    class="fixed inset-0 z-20 flex items-center justify-center bg-black/70 p-4"
    role="dialog"
    aria-modal="true"
  >
    <section
      data-testid="key-test-results"
      class="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-xl border border-slate-700 bg-slate-900 p-5 shadow-2xl"
    >
      <div class="flex items-center justify-between gap-4">
        <h2 class="text-lg font-semibold">
          NVIDIA Key 测试结果
        </h2>
        <button
          class="text-slate-400 hover:text-white"
          type="button"
          @click="emit('close')"
        >
          关闭
        </button>
      </div>
      <article
        v-for="result in results"
        :key="result.id"
        class="mt-4 rounded-lg border border-slate-800 p-4 first:mt-5"
      >
        <dl class="space-y-3 text-sm">
          <div class="flex justify-between gap-4">
            <dt class="text-slate-400">
              Key ID
            </dt>
            <dd>#{{ result.id }}</dd>
          </div>
          <div class="flex justify-between gap-4">
            <dt class="text-slate-400">
              状态
            </dt>
            <dd>{{ result.status }}</dd>
          </div>
          <div
            v-if="result.reason"
            class="flex justify-between gap-4"
          >
            <dt class="text-slate-400">
              原因
            </dt>
            <dd class="text-right text-slate-300">
              {{ result.reason }}
            </dd>
          </div>
          <div
            v-if="result.request_id"
            class="flex justify-between gap-4"
          >
            <dt class="text-slate-400">
              Request ID
            </dt>
            <dd class="font-mono text-right">
              {{ result.request_id }}
            </dd>
          </div>
          <div
            v-if="result.models?.length"
            class="flex justify-between gap-4"
          >
            <dt class="text-slate-400">
              模型数
            </dt>
            <dd>{{ result.models.length }}</dd>
          </div>
        </dl>
      </article>
    </section>
  </div>
</template>
