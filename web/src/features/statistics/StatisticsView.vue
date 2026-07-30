<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../../shared/api/client'
import { statisticsApi } from './api'
import type { DailyStatistic, RecentError, StatisticsDimension } from './types'

const dimensions: Array<{ type: StatisticsDimension; title: string }> = [
  { type: 'global', title: '总体' },
  { type: 'model', title: '模型' },
  { type: 'nvidia_key', title: 'NVIDIA Key' },
  { type: 'access_key', title: 'Access Key' },
]

const statistics = ref<DailyStatistic[]>([])
const recentErrors = ref<RecentError[]>([])
const loading = ref(false)
const statisticsError = ref('')
const errorsError = ref('')

const statisticsByDimension = computed<Record<StatisticsDimension, DailyStatistic[]>>(() => ({
  global: statistics.value.filter((item) => item.dimension_type === 'global'),
  model: statistics.value.filter((item) => item.dimension_type === 'model'),
  nvidia_key: statistics.value.filter((item) => item.dimension_type === 'nvidia_key'),
  access_key: statistics.value.filter((item) => item.dimension_type === 'access_key'),
}))

onMounted(() => {
  void loadStatistics()
})

async function loadStatistics(): Promise<void> {
  loading.value = true
  await Promise.all([loadDaily(), loadRecentErrors()])
  loading.value = false
}

async function loadDaily(): Promise<void> {
  try {
    statistics.value = (await statisticsApi.getDaily(30)).data
    statisticsError.value = ''
  } catch (error) {
    statisticsError.value = error instanceof ApiError ? error.message : '统计数据加载失败。'
  }
}

async function loadRecentErrors(): Promise<void> {
  try {
    recentErrors.value = (await statisticsApi.getRecentErrors(50)).data
    errorsError.value = ''
  } catch (error) {
    errorsError.value = error instanceof ApiError ? error.message : '最近错误加载失败。'
  }
}

function successRate(item: DailyStatistic): string {
  if (item.request_count === 0) return '—'
  return `${((item.success_count / item.request_count) * 100).toFixed(1)}%`
}

function formatAverage(value: number, unit = ''): string {
  return `${value.toFixed(1)}${unit}`
}

function formatAttempts(value: number): string {
  return value.toFixed(2)
}

function formatTokens(item: DailyStatistic): string {
  if (item.prompt_tokens === 0 && item.completion_tokens === 0) return '—'
  return `${item.prompt_tokens} / ${item.completion_tokens}`
}

function dimensionLabel(item: DailyStatistic): string {
  return item.dimension_type === 'global' ? '全部请求' : item.dimension_id
}

function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getUTCFullYear()}/${pad(date.getUTCMonth() + 1)}/${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}
</script>

<template>
  <main class="min-h-screen bg-slate-950 p-4 text-slate-100 sm:p-6">
    <section class="mx-auto max-w-7xl">
      <header class="rounded-xl bg-slate-900 px-5 py-5 shadow-xl sm:px-6">
        <p class="text-sm text-indigo-300">
          最近 30 天
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          基础统计
        </h1>
        <p class="mt-2 text-sm text-slate-400">
          按日期和四个安全维度查看请求聚合，不包含请求或响应正文。
        </p>
      </header>

      <p
        v-if="statisticsError"
        class="mt-4 text-sm text-rose-300"
        role="alert"
      >
        {{ statisticsError }}
      </p>
      <div
        v-if="loading"
        class="mt-5 rounded-xl border border-slate-800 bg-slate-900 p-6 text-sm text-slate-400"
      >
        加载中……
      </div>
      <template v-else>
        <section
          v-for="dimension in dimensions"
          :key="dimension.type"
          :data-testid="`statistics-${dimension.type}`"
          class="mt-5 overflow-hidden rounded-xl border border-slate-800 bg-slate-900"
        >
          <div class="border-b border-slate-800 px-5 py-4">
            <h2 class="font-medium">
              {{ dimension.title }}
            </h2>
          </div>
          <div
            v-if="statisticsByDimension[dimension.type].length === 0"
            class="p-5 text-sm text-slate-500"
          >
            暂无数据。
          </div>
          <div
            v-else
            class="overflow-x-auto"
          >
            <table class="min-w-full text-left text-sm">
              <thead class="bg-slate-950/60 text-xs text-slate-400">
                <tr>
                  <th class="px-4 py-3">
                    日期
                  </th>
                  <th class="px-4 py-3">
                    维度
                  </th>
                  <th class="px-4 py-3">
                    请求
                  </th>
                  <th class="px-4 py-3">
                    成功 / 失败
                  </th>
                  <th class="px-4 py-3">
                    成功率
                  </th>
                  <th class="px-4 py-3">
                    平均耗时
                  </th>
                  <th class="px-4 py-3">
                    平均排队
                  </th>
                  <th class="px-4 py-3">
                    平均尝试
                  </th>
                  <th class="px-4 py-3">
                    输入 / 输出 token
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-800">
                <tr
                  v-for="item in statisticsByDimension[dimension.type]"
                  :key="`${item.day}-${item.dimension_id}`"
                >
                  <td class="px-4 py-3">
                    {{ item.day }}
                  </td>
                  <td class="px-4 py-3 font-mono text-indigo-200">
                    {{ dimensionLabel(item) }}
                  </td>
                  <td class="px-4 py-3">
                    {{ item.request_count }}
                  </td>
                  <td class="px-4 py-3">
                    {{ item.success_count }} / {{ item.failure_count }}
                  </td>
                  <td class="px-4 py-3">
                    {{ successRate(item) }}
                  </td>
                  <td class="px-4 py-3">
                    {{ formatAverage(item.average_duration_ms, ' ms') }}
                  </td>
                  <td class="px-4 py-3">
                    {{ formatAverage(item.average_queue_ms, ' ms') }}
                  </td>
                  <td class="px-4 py-3">
                    {{ formatAttempts(item.average_attempts) }}
                  </td>
                  <td class="px-4 py-3">
                    {{ formatTokens(item) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section
          data-testid="recent-errors"
          class="mt-5 overflow-hidden rounded-xl border border-slate-800 bg-slate-900"
        >
          <div class="border-b border-slate-800 px-5 py-4">
            <h2 class="font-medium">
              最近安全错误
            </h2>
            <p class="mt-1 text-sm text-slate-400">
              仅显示请求 ID、路由、维度 ID、状态码和错误分类。
            </p>
          </div>
          <p
            v-if="errorsError"
            class="p-5 text-sm text-rose-300"
          >
            {{ errorsError }}
          </p>
          <p
            v-else-if="recentErrors.length === 0"
            class="p-5 text-sm text-slate-500"
          >
            暂无错误。
          </p>
          <div
            v-else
            class="overflow-x-auto"
          >
            <table class="min-w-full text-left text-sm">
              <thead class="bg-slate-950/60 text-xs text-slate-400">
                <tr>
                  <th class="px-4 py-3">
                    时间
                  </th>
                  <th class="px-4 py-3">
                    请求 ID
                  </th>
                  <th class="px-4 py-3">
                    接口 / 模型
                  </th>
                  <th class="px-4 py-3">
                    Key ID
                  </th>
                  <th class="px-4 py-3">
                    状态 / 分类
                  </th>
                  <th class="px-4 py-3">
                    上游请求 ID
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-800">
                <tr
                  v-for="item in recentErrors"
                  :key="`${item.created_at}-${item.request_id}`"
                >
                  <td class="px-4 py-3">
                    {{ formatDate(item.created_at) }}
                  </td>
                  <td class="px-4 py-3 font-mono">
                    {{ item.request_id }}
                  </td>
                  <td class="px-4 py-3">
                    <span class="block">{{ item.endpoint }}</span>
                    <span class="font-mono text-xs text-slate-400">{{ item.model_id ?? '—' }}</span>
                  </td>
                  <td class="px-4 py-3 text-xs">
                    <span class="block">NVIDIA: {{ item.nvidia_key_id ?? '—' }}</span>
                    <span class="block">Access: {{ item.access_key_id ?? '—' }}</span>
                  </td>
                  <td class="px-4 py-3">
                    <span class="block">{{ item.http_status }}</span>
                    <span class="font-mono text-xs text-rose-300">{{ item.error_code }}</span>
                  </td>
                  <td class="px-4 py-3 font-mono text-xs">
                    {{ item.upstream_request_id ?? '—' }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </section>
  </main>
</template>
