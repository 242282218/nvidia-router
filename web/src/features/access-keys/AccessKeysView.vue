<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isDataArrayResponse, isFiniteNumber, isRecord } from '../../shared/api/client'
import { accessKeysApi } from './api'
import CreateAccessKeyDialog from './CreateAccessKeyDialog.vue'
import type { AccessKey } from './types'

const keys = ref<AccessKey[]>([])
const loading = ref(false)
const dialogOpen = ref(false)
const busyId = ref<number | null>(null)
const errorMessage = ref('')
let loadSequence = 0
let disposed = false

onMounted(() => {
  void loadKeys()
})

onBeforeUnmount(() => {
  disposed = true
  loadSequence += 1
})

async function loadKeys(): Promise<void> {
  if (disposed) return
  const sequence = ++loadSequence
  loading.value = true
  try {
    const response: unknown = await accessKeysApi.list()
    if (disposed || sequence !== loadSequence) return
    if (!isDataArrayResponse(response, isAccessKey)) {
      throw new TypeError('Invalid Access Key list response.')
    }
    keys.value = response.data
    errorMessage.value = ''
  } catch (error) {
    if (disposed || sequence !== loadSequence) return
    errorMessage.value = error instanceof ApiError ? error.message : 'Access Key 列表加载失败。'
  } finally {
    if (!disposed && sequence === loadSequence) loading.value = false
  }
}

function isAccessKey(value: unknown): value is AccessKey {
  return isRecord(value)
    && isFiniteNumber(value.id)
    && typeof value.name === 'string'
    && typeof value.key_prefix === 'string'
    && typeof value.created_at === 'string'
    && isOptionalString(value.last_used_at)
    && isOptionalString(value.revoked_at)
}

function isOptionalString(value: unknown): boolean {
  return value === undefined || typeof value === 'string'
}

async function revokeKey(key: AccessKey): Promise<void> {
  if (!globalThis.window.confirm(`确认撤销 Access Key“${key.name}”吗？撤销后无法恢复。`)) return
  busyId.value = key.id
  errorMessage.value = ''
  try {
    await accessKeysApi.revoke(key.id)
    if (disposed) return
    await loadKeys()
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : 'Access Key 撤销失败。'
  } finally {
    if (!disposed) busyId.value = null
  }
}

function formatDate(value?: string): string {
  if (!value) return '从未使用'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-4 text-slate-100 sm:p-6">
    <section class="mx-auto max-w-6xl">
      <header class="rounded-xl bg-slate-900 px-5 py-5 shadow-xl sm:px-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-sm text-indigo-300">
              安全管理
            </p>
            <h1 class="mt-1 text-2xl font-semibold">
              Access Key
            </h1>
            <p class="mt-2 text-sm text-slate-400">
              管理调用路由器的下游设备和客户端凭证。
            </p>
          </div>
          <button
            data-testid="open-create-access-key"
            class="rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white"
            type="button"
            @click="dialogOpen = true"
          >
            创建 Access Key
          </button>
        </div>
      </header>

      <p
        v-if="errorMessage"
        class="mt-4 text-sm text-rose-300"
        role="alert"
      >
        {{ errorMessage }}
      </p>

      <section class="mt-5 overflow-hidden rounded-xl border border-slate-800 bg-slate-900">
        <div
          v-if="loading"
          class="p-6 text-sm text-slate-400"
        >
          加载中……
        </div>
        <div
          v-else-if="keys.length === 0"
          class="p-6 text-sm text-slate-400"
        >
          尚未创建 Access Key。
        </div>
        <div
          v-else
          class="overflow-x-auto"
        >
          <div
            data-testid="access-key-cards"
            class="space-y-3 p-4 md:hidden"
          >
            <article
              v-for="key in keys"
              :key="`card-${key.id}`"
              class="rounded-lg border border-slate-800 p-4"
            >
              <div class="flex items-start justify-between gap-3">
                <div>
                  <h2 class="font-medium">
                    {{ key.name }}
                  </h2>
                  <p class="mt-1 font-mono text-xs text-indigo-200">
                    {{ key.key_prefix }}
                  </p>
                </div>
                <span :class="key.revoked_at ? 'text-slate-500' : 'text-emerald-300'">
                  {{ key.revoked_at ? '已撤销' : '有效' }}
                </span>
              </div>
              <button
                :data-testid="`mobile-revoke-access-key-${key.id}`"
                class="mt-4 w-full rounded border border-rose-700 px-3 py-2 text-sm text-rose-300 disabled:opacity-40"
                type="button"
                :disabled="Boolean(key.revoked_at) || busyId === key.id"
                @click="revokeKey(key)"
              >
                撤销
              </button>
            </article>
          </div>
          <table
            data-testid="access-key-table"
            class="hidden min-w-full text-left text-sm md:table"
          >
            <thead class="bg-slate-950/60 text-xs uppercase text-slate-400">
              <tr>
                <th class="px-4 py-3">
                  名称
                </th>
                <th class="px-4 py-3">
                  前缀
                </th>
                <th class="px-4 py-3">
                  创建时间
                </th>
                <th class="px-4 py-3">
                  最后使用
                </th>
                <th class="px-4 py-3">
                  状态
                </th>
                <th class="px-4 py-3 text-right">
                  操作
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-800">
              <tr
                v-for="key in keys"
                :key="key.id"
              >
                <td class="px-4 py-3 font-medium">
                  {{ key.name }}
                </td>
                <td class="px-4 py-3 font-mono text-indigo-200">
                  {{ key.key_prefix }}
                </td>
                <td class="px-4 py-3 text-slate-300">
                  {{ formatDate(key.created_at) }}
                </td>
                <td class="px-4 py-3 text-slate-300">
                  {{ formatDate(key.last_used_at) }}
                </td>
                <td
                  class="px-4 py-3"
                  :class="key.revoked_at ? 'text-slate-500' : 'text-emerald-300'"
                >
                  {{ key.revoked_at ? '已撤销' : '有效' }}
                </td>
                <td class="px-4 py-3 text-right">
                  <button
                    :data-testid="`revoke-access-key-${key.id}`"
                    class="rounded-lg border border-rose-700 px-3 py-1.5 text-xs text-rose-300 disabled:opacity-40"
                    type="button"
                    :disabled="Boolean(key.revoked_at) || busyId === key.id"
                    @click="revokeKey(key)"
                  >
                    撤销
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </section>

    <CreateAccessKeyDialog
      :open="dialogOpen"
      @close="dialogOpen = false"
      @created="loadKeys"
    />
  </main>
</template>
