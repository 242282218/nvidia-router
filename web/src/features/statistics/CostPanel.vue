<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
import { statisticsApi } from './api'
import type { DailyModelCost } from './types'

const costs = ref<DailyModelCost[]>([])
const loading = ref(true)
const errorMessage = ref('')
let disposed = false

onMounted(() => {
  void load()
})

onBeforeUnmount(() => {
  disposed = true
})

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const response: unknown = await statisticsApi.getCosts(30)
    if (disposed) return
    if (!isCostPage(response)) throw new TypeError('Invalid cost response.')
    costs.value = response.data
  } catch (error) {
    if (disposed) return
    errorMessage.value = error instanceof ApiError ? error.message : '成本估算加载失败。'
  } finally {
    if (!disposed) loading.value = false
  }
}

function isCostPage(value: unknown): value is { data: DailyModelCost[] } {
  return isRecord(value)
    && Array.isArray(value.data)
    && value.data.every(isDailyModelCost)
}

function isDailyModelCost(value: unknown): value is DailyModelCost {
  return isRecord(value)
    && typeof value.day === 'string'
    && typeof value.model_id === 'string'
    && isFiniteNumber(value.prompt_tokens)
    && isFiniteNumber(value.completion_tokens)
    && isFiniteNumber(value.total_cost_usd)
    && typeof value.priced === 'boolean'
}

const totalCost = computed(() => costs.value.reduce((sum, item) => sum + item.total_cost_usd, 0))
const unpricedCount = computed(() => new Set(costs.value.filter((item) => !item.priced).map((item) => item.model_id)).size)
const totalTokens = computed(() => costs.value.reduce((sum, item) => sum + item.prompt_tokens + item.completion_tokens, 0))

const byModel = computed(() => {
  const map = new Map<string, { cost: number; tokens: number; priced: boolean }>()
  for (const item of costs.value) {
    const entry = map.get(item.model_id) ?? { cost: 0, tokens: 0, priced: false }
    entry.cost += item.total_cost_usd
    entry.tokens += item.prompt_tokens + item.completion_tokens
    entry.priced = entry.priced || item.priced
    map.set(item.model_id, entry)
  }
  return [...map.entries()].sort((a, b) => b[1].cost - a[1].cost)
})

function formatUSD(value: number): string {
  if (value < 0.01) return `$${value.toFixed(4)}`
  return `$${value.toFixed(2)}`
}
</script>

<template>
  <div class="card mt-4 p-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-sm font-medium text-[var(--color-text)]">
          成本估算（近 30 天）
        </h2>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          基于请求 Token 用量与模型单价（USD /1M）估算；单价为空按 $0 计。
        </p>
      </div>
      <button
        class="btn-ghost rounded-lg px-3 py-1.5 text-xs"
        type="button"
        @click="load"
      >
        刷新
      </button>
    </div>

    <p
      v-if="errorMessage"
      class="mt-3 flex flex-wrap items-center gap-3 text-sm text-[var(--color-danger)]"
      role="alert"
    >
      <span>{{ errorMessage }}</span>
      <button
        class="btn-secondary rounded-lg px-3 py-1 text-xs"
        type="button"
        :disabled="loading"
        @click="load"
      >
        重试
      </button>
    </p>

    <div
      v-if="loading"
      class="mt-3 text-sm text-[var(--color-text-muted)]"
    >
      加载成本数据…
    </div>

    <template v-else-if="costs.length">
      <div class="mt-4 grid gap-3 sm:grid-cols-3">
        <div class="stat-card">
          <p class="text-xs text-[var(--color-text-muted)]">
            估算成本
          </p>
          <p class="mt-2 text-2xl font-semibold text-[var(--color-text)]">
            {{ formatUSD(totalCost) }}
          </p>
        </div>
        <div class="stat-card">
          <p class="text-xs text-[var(--color-text-muted)]">
            总 Token
          </p>
          <p class="mt-2 text-2xl font-semibold text-[var(--color-text)]">
            {{ totalTokens.toLocaleString('zh-CN') }}
          </p>
        </div>
        <div class="stat-card">
          <p class="text-xs text-[var(--color-text-muted)]">
            未定价模型
          </p>
          <p
            class="mt-2 text-2xl font-semibold"
            :class="unpricedCount > 0 ? 'text-[var(--color-warning)]' : 'text-[var(--color-success)]'"
          >
            {{ unpricedCount }}
          </p>
        </div>
      </div>

      <div
        v-if="unpricedCount > 0"
        class="mt-3 rounded-lg border border-[var(--color-warning)]/25 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]"
      >
        有 {{ unpricedCount }} 个模型未设置单价，可在「模型白名单」页补充后让成本估算更准确。
      </div>

      <div class="mt-4 overflow-x-auto">
        <table class="data-table min-w-[560px]">
          <thead>
            <tr>
              <th class="data-table-th">
                模型
              </th>
              <th class="data-table-th text-right">
                Token
              </th>
              <th class="data-table-th text-right">
                估算成本
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-[var(--color-border)]">
            <tr
              v-for="[modelId, entry] in byModel"
              :key="modelId"
            >
              <td class="data-table-td">
                <code class="font-mono text-xs text-[var(--color-info)]">{{ modelId }}</code>
                <span
                  v-if="!entry.priced"
                  class="ml-2 badge-warning"
                >未定价</span>
              </td>
              <td class="data-table-td text-right font-mono text-xs text-[var(--color-text-secondary)]">
                {{ entry.tokens.toLocaleString('zh-CN') }}
              </td>
              <td class="data-table-td text-right text-[var(--color-text)]">
                {{ formatUSD(entry.cost) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <div
      v-else-if="!errorMessage"
      class="mt-3 text-sm text-[var(--color-text-muted)]"
    >
      近 30 天暂无 Token 用量记录。
    </div>
  </div>
</template>
