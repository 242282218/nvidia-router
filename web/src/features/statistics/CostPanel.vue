<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import { statisticsApi } from './api'
import type { DailyModelCost } from './types'

const dayOptions: Array<{ value: 7 | 30 | 90; label: string }> = [
  { value: 7, label: '近 7 天' },
  { value: 30, label: '近 30 天' },
  { value: 90, label: '近 90 天' },
]

const selectedDays = ref<7 | 30 | 90>(30)
const currencyMode = ref<'BOTH' | 'USD' | 'CNY'>('BOTH')
const USD_TO_CNY = 7.23

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
    const response: unknown = await statisticsApi.getCosts(selectedDays.value)
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

function selectDays(days: 7 | 30 | 90): void {
  if (selectedDays.value === days) return
  selectedDays.value = days
  void load()
}

function toggleCurrency(): void {
  if (currencyMode.value === 'BOTH') currencyMode.value = 'CNY'
  else if (currencyMode.value === 'CNY') currencyMode.value = 'USD'
  else currencyMode.value = 'BOTH'
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

const totalCostUSD = computed(() => costs.value.reduce((sum, item) => sum + item.total_cost_usd, 0))
const totalPromptCostUSD = computed(() => costs.value.reduce((sum, item) => sum + (item.input_cost_usd || 0), 0))
const totalCompletionCostUSD = computed(() => costs.value.reduce((sum, item) => sum + (item.output_cost_usd || 0), 0))

const promptTokens = computed(() => costs.value.reduce((sum, item) => sum + item.prompt_tokens, 0))
const completionTokens = computed(() => costs.value.reduce((sum, item) => sum + item.completion_tokens, 0))
const totalTokens = computed(() => promptTokens.value + completionTokens.value)

const unpricedCount = computed(() => new Set(costs.value.filter((item) => !item.priced).map((item) => item.model_id)).size)

interface ModelAggregate {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  inputCostUSD: number
  outputCostUSD: number
  totalCostUSD: number
  priced: boolean
  sharePercent: number
}

const byModel = computed(() => {
  const map = new Map<string, { promptTokens: number; completionTokens: number; inputCostUSD: number; outputCostUSD: number; totalCostUSD: number; priced: boolean }>()
  for (const item of costs.value) {
    const entry = map.get(item.model_id) ?? {
      promptTokens: 0,
      completionTokens: 0,
      inputCostUSD: 0,
      outputCostUSD: 0,
      totalCostUSD: 0,
      priced: false,
    }
    entry.promptTokens += item.prompt_tokens
    entry.completionTokens += item.completion_tokens
    entry.inputCostUSD += item.input_cost_usd || 0
    entry.outputCostUSD += item.output_cost_usd || 0
    entry.totalCostUSD += item.total_cost_usd
    entry.priced = entry.priced || item.priced
    map.set(item.model_id, entry)
  }
  const total = totalCostUSD.value
  const result: Array<[string, ModelAggregate]> = []
  for (const [modelId, entry] of map.entries()) {
    const totalTok = entry.promptTokens + entry.completionTokens
    const share = total > 0 ? (entry.totalCostUSD / total) * 100 : 0
    result.push([modelId, {
      ...entry,
      totalTokens: totalTok,
      sharePercent: Number(share.toFixed(1)),
    }])
  }
  return result.sort((a, b) => b[1].totalCostUSD - a[1].totalCostUSD)
})

function formatUSD(value: number): string {
  if (value === 0) return '$0.00'
  if (value < 0.0001) return `$${value.toFixed(6)}`
  if (value < 0.01) return `$${value.toFixed(4)}`
  return `$${value.toFixed(2)}`
}

function formatCNY(usd: number): string {
  const cny = usd * USD_TO_CNY
  if (cny === 0) return '¥0.00'
  if (cny < 0.0001) return `¥${cny.toFixed(6)}`
  if (cny < 0.01) return `¥${cny.toFixed(4)}`
  return `¥${cny.toFixed(2)}`
}

function formatCost(usd: number): string {
  if (currencyMode.value === 'USD') return formatUSD(usd)
  if (currencyMode.value === 'CNY') return formatCNY(usd)
  return `${formatUSD(usd)} / ${formatCNY(usd)}`
}

function formatCompactTokens(value: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}
</script>

<template>
  <div
    data-testid="cost-panel"
    class="card mt-4 p-5"
  >
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-sm font-semibold text-[var(--color-text)]">
          成本估算与用量分析
        </h2>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          基于请求 Token 用量与模型单价（USD /1M）实时估算，汇率按 1 USD ≈ {{ USD_TO_CNY }} CNY 换算。
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <div
          class="inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-[var(--shadow-xs)]"
          role="group"
          aria-label="成本统计范围"
        >
          <button
            v-for="opt in dayOptions"
            :key="opt.value"
            :data-testid="`cost-range-${opt.value}`"
            class="h-8 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color,box-shadow] duration-[var(--duration-micro)]"
            :class="selectedDays === opt.value ? 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-[var(--shadow-xs)]' : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)]'"
            type="button"
            :aria-pressed="selectedDays === opt.value"
            @click="selectDays(opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>
        <button
          class="btn-secondary px-2.5 py-1 text-xs"
          type="button"
          data-testid="cost-currency-toggle"
          @click="toggleCurrency"
        >
          币种：{{ currencyMode === 'BOTH' ? '双币 (USD/CNY)' : currencyMode === 'CNY' ? '人民币 (CNY)' : '美元 (USD)' }}
        </button>
        <button
          class="btn-ghost rounded-lg px-2.5 py-1 text-xs"
          type="button"
          :disabled="loading"
          @click="load"
        >
          {{ loading ? '刷新中…' : '刷新' }}
        </button>
      </div>
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
      class="mt-4"
      role="status"
      aria-busy="true"
      aria-label="加载成本分析数据…"
    >
      <UiSkeleton
        variant="cards"
        :lines="4"
      />
    </div>

    <template v-else-if="costs.length">
      <!-- KPI cards grid -->
      <div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="stat-card">
          <p class="text-xs font-medium text-[var(--color-text-muted)]">
            总估算成本（近 {{ selectedDays }} 天）
          </p>
          <p class="mt-2 font-mono-data text-2xl font-semibold tabular-nums text-[var(--color-text)]">
            {{ formatUSD(totalCostUSD) }}
          </p>
          <p class="mt-1 font-mono-data text-xs text-[var(--color-text-secondary)]">
            ≈ {{ formatCNY(totalCostUSD) }}
          </p>
        </div>

        <div class="stat-card">
          <p class="text-xs font-medium text-[var(--color-text-muted)]">
            输入 / 输出费用拆解
          </p>
          <div class="mt-2 flex items-baseline justify-between font-mono-data text-sm">
            <span class="text-[var(--color-text-secondary)]">输入</span>
            <span class="font-semibold text-[var(--color-text)]">{{ formatUSD(totalPromptCostUSD) }}</span>
          </div>
          <div class="mt-1 flex items-baseline justify-between font-mono-data text-sm">
            <span class="text-[var(--color-text-secondary)]">输出</span>
            <span class="font-semibold text-[var(--color-text)]">{{ formatUSD(totalCompletionCostUSD) }}</span>
          </div>
        </div>

        <div class="stat-card">
          <p class="text-xs font-medium text-[var(--color-text-muted)]">
            总消耗 Token
          </p>
          <p class="mt-2 font-mono-data text-2xl font-semibold tabular-nums text-[var(--color-text)]">
            {{ formatCompactTokens(totalTokens) }}
          </p>
          <p class="mt-1 text-xs text-[var(--color-text-muted)]">
            入 {{ formatCompactTokens(promptTokens) }} · 出 {{ formatCompactTokens(completionTokens) }}
          </p>
        </div>

        <div class="stat-card">
          <p class="text-xs font-medium text-[var(--color-text-muted)]">
            模型定价覆盖
          </p>
          <p
            class="mt-2 text-2xl font-semibold"
            :class="unpricedCount > 0 ? 'text-[var(--color-warning)]' : 'text-[var(--color-success)]'"
          >
            {{ unpricedCount === 0 ? '全部已定价' : `${unpricedCount} 个未定价` }}
          </p>
          <p class="mt-1 text-xs text-[var(--color-text-muted)]">
            共计 {{ byModel.length }} 个活跃模型
          </p>
        </div>
      </div>

      <!-- Cost distribution visual bar -->
      <div
        v-if="totalCostUSD > 0 && byModel.length > 0"
        class="mt-4 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-3"
      >
        <div class="flex items-center justify-between text-xs font-medium text-[var(--color-text-secondary)]">
          <span>模型花费占比分布</span>
          <span>总花费 {{ formatUSD(totalCostUSD) }}</span>
        </div>
        <div class="mt-2 flex h-2.5 w-full overflow-hidden rounded-full bg-[var(--color-border)]">
          <div
            v-for="[modelId, entry] in byModel.slice(0, 6)"
            :key="modelId"
            class="h-full transition-all first:rounded-l-full last:rounded-r-full"
            :style="{ width: `${Math.max(entry.sharePercent, 2)}%`, backgroundColor: entry.totalCostUSD > 0 ? undefined : 'transparent' }"
            :class="entry.sharePercent > 50 ? 'bg-[var(--color-accent)]' : entry.sharePercent > 20 ? 'bg-[var(--color-info)]' : entry.sharePercent > 10 ? 'bg-[var(--color-success)]' : 'bg-[var(--color-warning)]'"
            :title="`${modelId}: ${entry.sharePercent}% (${formatUSD(entry.totalCostUSD)})`"
          />
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-[var(--color-text-muted)]">
          <span
            v-for="[modelId, entry] in byModel.slice(0, 4)"
            :key="modelId"
            class="flex items-center gap-1.5"
          >
            <span class="h-2 w-2 rounded-full bg-[var(--color-info)]" />
            <code class="font-mono-data text-xs">{{ modelId }}</code>
            <strong class="font-mono-data text-[var(--color-text)]">{{ entry.sharePercent }}%</strong>
          </span>
        </div>
      </div>

      <div
        v-if="unpricedCount > 0"
        class="mt-3 rounded-lg border border-[color-mix(in_srgb,var(--color-warning)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] px-3 py-2 text-xs text-[var(--color-warning)]"
      >
        有 {{ unpricedCount }} 个模型未设置单价，可在「模型白名单」页配置输入与输出价格（USD/1M Tokens），完善成本核算。
      </div>

      <!-- Breakdown Table -->
      <div class="mt-4 overflow-x-auto">
        <table class="data-table min-w-[720px]">
          <caption class="sr-only">
            按模型聚合的 Token 用量与估算成本，近 {{ selectedDays }} 天
          </caption>
          <thead>
            <tr>
              <th
                class="data-table-th"
                scope="col"
              >
                模型
              </th>
              <th
                class="data-table-th text-right"
                scope="col"
              >
                输入 (Token / 费用)
              </th>
              <th
                class="data-table-th text-right"
                scope="col"
              >
                输出 (Token / 费用)
              </th>
              <th
                class="data-table-th text-right"
                scope="col"
              >
                总 Token
              </th>
              <th
                class="data-table-th text-right"
                scope="col"
              >
                估算总成本
              </th>
              <th
                class="data-table-th text-right"
                scope="col"
              >
                支出占比
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-[var(--color-border-subtle)]">
            <tr
              v-for="[modelId, entry] in byModel"
              :key="modelId"
              class="data-table-row"
            >
              <td class="data-table-td">
                <div class="flex items-center gap-2">
                  <code class="font-mono-data text-xs font-medium text-[var(--color-info)]">{{ modelId }}</code>
                  <span
                    v-if="!entry.priced"
                    class="badge-warning"
                  >未定价</span>
                </div>
              </td>
              <td class="data-table-td text-right font-mono-data text-xs">
                <p class="text-[var(--color-text)]">
                  {{ entry.promptTokens.toLocaleString('zh-CN') }}
                </p>
                <p class="text-[var(--color-text-muted)]">
                  {{ formatCost(entry.inputCostUSD) }}
                </p>
              </td>
              <td class="data-table-td text-right font-mono-data text-xs">
                <p class="text-[var(--color-text)]">
                  {{ entry.completionTokens.toLocaleString('zh-CN') }}
                </p>
                <p class="text-[var(--color-text-muted)]">
                  {{ formatCost(entry.outputCostUSD) }}
                </p>
              </td>
              <td class="data-table-td text-right font-mono-data text-xs font-medium text-[var(--color-text)]">
                {{ entry.totalTokens.toLocaleString('zh-CN') }}
              </td>
              <td class="data-table-td text-right font-mono-data text-xs font-semibold text-[var(--color-text)]">
                <p>{{ formatCost(entry.totalCostUSD) }}</p>
              </td>
              <td class="data-table-td text-right font-mono-data text-xs">
                <div class="flex items-center justify-end gap-2">
                  <div class="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--color-border)]">
                    <div
                      class="h-full rounded-full bg-[var(--color-accent)]"
                      :style="{ width: `${Math.min(100, Math.max(0, entry.sharePercent))}%` }"
                    />
                  </div>
                  <span class="w-10 text-right text-[var(--color-text-secondary)]">{{ entry.sharePercent }}%</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <div
      v-else-if="!errorMessage"
      class="mt-4 rounded-[var(--radius-panel)] border border-dashed border-[var(--color-border)] p-6 text-center text-sm text-[var(--color-text-muted)]"
    >
      近 {{ selectedDays }} 天暂无 Token 用量与请求记录。
    </div>
  </div>
</template>
