<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isFiniteNumber, isRecord } from '../../shared/api/client'
import UiSkeleton from '../../shared/ui/UiSkeleton.vue'
import { statisticsApi } from './api'
import type { DailyModelCost } from './types'

// 模型成本排行（设计 §5.4）：原 CostPanel 的多块看板收敛为 ranking list——
// 名次徽章 + 模型 + 成本 + 占比条。数据加载、聚合与币种折算逻辑不变。
defineOptions({ name: 'CostPanel' })

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

const unpricedCount = computed(() => new Set(costs.value.filter((item) => !item.priced).map((item) => item.model_id)).size)

interface ModelAggregate {
  totalTokens: number
  totalCostUSD: number
  priced: boolean
  sharePercent: number
}

const byModel = computed(() => {
  const map = new Map<string, { promptTokens: number; completionTokens: number; totalCostUSD: number; priced: boolean }>()
  for (const item of costs.value) {
    const entry = map.get(item.model_id) ?? { promptTokens: 0, completionTokens: 0, totalCostUSD: 0, priced: false }
    entry.promptTokens += item.prompt_tokens
    entry.completionTokens += item.completion_tokens
    entry.totalCostUSD += item.total_cost_usd
    entry.priced = entry.priced || item.priced
    map.set(item.model_id, entry)
  }
  const total = totalCostUSD.value
  const result: Array<[string, ModelAggregate]> = []
  for (const [modelId, entry] of map.entries()) {
    const share = total > 0 ? (entry.totalCostUSD / total) * 100 : 0
    result.push([modelId, {
      totalTokens: entry.promptTokens + entry.completionTokens,
      totalCostUSD: entry.totalCostUSD,
      priced: entry.priced,
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

// 名次徽章：1=accent、2=info、3=warning、其余弱化为文本序号（CPA §43）。
function rankClass(rank: number): string {
  if (rank === 1) return 'bg-[var(--color-accent-background)] text-[var(--color-accent-foreground)]'
  if (rank === 2) return 'bg-[var(--color-info-background)] text-[var(--color-info-foreground)]'
  if (rank === 3) return 'bg-[var(--color-warning-background)] text-[var(--color-warning-foreground)]'
  return 'bg-[var(--color-muted-background)] text-[var(--color-muted-foreground)]'
}

// 第一名占比条用 accent 强调，其余统一低饱和（色块只做视觉锚点，数值由文字承载）。
function barColor(rank: number): string {
  return rank === 1 ? 'var(--color-accent)' : 'color-mix(in srgb, var(--color-text-subtle) 30%, transparent)'
}
</script>

<template>
  <section
    data-testid="cost-panel"
    class="card flex flex-col overflow-hidden"
    aria-label="模型成本排行"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <div>
        <h3 class="type-heading">
          模型成本排行
        </h3>
        <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
          Token 用量 × 单价估算 · 汇率 1 USD ≈ {{ USD_TO_CNY }} CNY
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
      class="m-4 flex flex-wrap items-center gap-3 text-sm text-[var(--color-danger)]"
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
      v-else-if="loading"
      class="m-4"
      role="status"
      aria-busy="true"
      aria-label="加载成本数据…"
    >
      <UiSkeleton
        variant="cards"
        :lines="4"
      />
    </div>

    <template v-else-if="byModel.length">
      <div
        class="flex flex-wrap items-baseline justify-between gap-2 px-4 py-2.5 text-xs text-[var(--color-text-muted)]"
        data-testid="cost-panel-total"
      >
        <span>总成本（近 {{ selectedDays }} 天）</span>
        <span class="font-mono-data text-sm font-semibold text-[var(--color-text)]">
          {{ formatCost(totalCostUSD) }}
        </span>
      </div>

      <ol class="divide-y divide-[var(--color-border-subtle)]">
        <li
          v-for="(entry, index) in byModel"
          :key="entry[0]"
          class="px-4 py-2.5"
          data-testid="cost-ranking-row"
        >
          <div class="flex items-center gap-2.5">
            <span
              class="flex h-6 w-6 shrink-0 items-center justify-center rounded-[5px] font-mono-data text-xs font-semibold"
              :class="rankClass(index + 1)"
              aria-hidden="true"
            >{{ index + 1 }}</span>
            <span class="min-w-0 flex-1 truncate font-mono-data text-xs font-medium text-[var(--color-text)]">
              {{ entry[0] }}
            </span>
            <span
              v-if="!entry[1].priced"
              class="badge-warning"
            >未定价</span>
            <span class="shrink-0 font-mono-data text-xs font-semibold text-[var(--color-text)]">
              {{ formatCost(entry[1].totalCostUSD) }}
            </span>
            <span class="w-11 shrink-0 text-right font-mono-data text-xs text-[var(--color-text-muted)]">
              {{ entry[1].sharePercent }}%
            </span>
          </div>
          <div class="mt-1.5 flex items-center gap-2 pl-[34px]">
            <div class="h-[5px] flex-1 overflow-hidden rounded-full bg-[var(--color-sunken)]">
              <div
                class="h-full rounded-full"
                :style="{ width: `${Math.min(100, Math.max(0, entry[1].sharePercent))}%`, background: barColor(index + 1) }"
              />
            </div>
            <span class="shrink-0 font-mono-data text-xs text-[var(--color-text-subtle)]">
              {{ formatCompactTokens(entry[1].totalTokens) }} tok
            </span>
          </div>
        </li>
      </ol>

      <div
        v-if="unpricedCount > 0"
        class="mx-4 mb-4 mt-3 rounded-lg border border-[color-mix(in_srgb,var(--color-warning)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] px-3 py-2 text-xs text-[var(--color-warning)]"
      >
        有 {{ unpricedCount }} 个模型未设置单价，可在「模型白名单」页配置输入与输出价格（USD/1M Tokens），完善成本核算。
      </div>
    </template>

    <div
      v-else
      class="m-4 rounded-[var(--radius-panel)] border border-dashed border-[var(--color-border)] p-6 text-center text-sm text-[var(--color-text-muted)]"
    >
      近 {{ selectedDays }} 天暂无 Token 用量与请求记录。
    </div>
  </section>
</template>
