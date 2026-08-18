<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError, isFiniteNumber, isRecord } from '../../shared/api/client'
import { formatLocalDateTime } from '../../shared/format'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import UiStat from '../../shared/ui/UiStat.vue'
import UiStatePanel from '../../shared/ui/UiStatePanel.vue'
import { usePolling } from '../../shared/usePolling'
import { proxyPoolApi } from './api'
import type { PoolStatusData, ProxyPoolPatch, ProxyPoolSettings } from './types'

const settings = ref<ProxyPoolSettings | null>(null)
const enabled = ref(false)
const upstreamURL = ref('')
const validationUrl = ref('')
const validationStatus = ref(404)
const interval = ref('5s')
const proxyTTL = ref('120s')
const expectedQty = ref(2)
const concurrency = ref(2)
const maxLatency = ref('')
const loading = ref(false)
const saving = ref(false)
const refreshing = ref(false)
const errorMessage = ref('')
const formError = ref('')
const savedMessage = ref('')
const statusData = ref<PoolStatusData | null>(null)
const statusLoading = ref(false)
const statusError = ref('')
const statusUpdatedAt = ref<Date | null>(null)
let disposed = false
let controller: AbortController | null = null
let statusController: AbortController | null = null

onMounted(() => {
  void loadSettings()
  void refreshStatus()
})

// Pool status telemetry refreshes every 10s, paused while the tab is hidden.
usePolling(() => refreshStatus(), 10_000)

onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  statusController?.abort()
})

async function loadSettings(): Promise<void> {
  controller?.abort()
  const next = new AbortController()
  controller = next
  loading.value = true
  try {
    applySettings((await proxyPoolApi.get(next.signal)).data)
    errorMessage.value = ''
  } catch (error) {
    if (!disposed && !isAbortError(error)) {
      errorMessage.value = error instanceof ApiError ? error.message : '代理池配置加载失败。'
    }
  } finally {
    if (!disposed && controller === next) loading.value = false
  }
}

async function refreshStatus(): Promise<void> {
  if (disposed) return
  statusController?.abort()
  const next = new AbortController()
  statusController = next
  statusLoading.value = true
  try {
    const response: unknown = await proxyPoolApi.status(next.signal)
    if (disposed || statusController !== next) return
    if (!isPoolStatusData(response)) throw new TypeError('Invalid proxy pool status response.')
    statusData.value = response.data
    statusUpdatedAt.value = new Date()
    statusError.value = ''
  } catch (error) {
    if (!disposed && statusController === next && !isAbortError(error)) {
      statusError.value = error instanceof ApiError ? error.message : '代理池状态加载失败。'
    }
  } finally {
    if (!disposed && statusController === next) statusLoading.value = false
  }
}

function isPoolStatusData(value: unknown): value is { data: PoolStatusData } {
  if (!isRecord(value) || !isRecord(value.data)) return false
  const data = value.data
  return (data.configured === undefined || typeof data.configured === 'boolean')
    && (data.mode === undefined || typeof data.mode === 'string')
    && (data.healthy_size === undefined || isNonNegativeNumber(data.healthy_size))
}

function isNonNegativeNumber(value: unknown): value is number {
  return isFiniteNumber(value) && value >= 0
}

function applySettings(next: ProxyPoolSettings): void {
  settings.value = next
  enabled.value = next.enabled
  upstreamURL.value = ''
  validationUrl.value = next.validation_url ?? ''
  validationStatus.value = next.validation_status ?? 404
  interval.value = next.collector_interval ?? '5s'
  proxyTTL.value = next.proxy_ttl ?? '120s'
  expectedQty.value = next.expected_qty ?? 2
  concurrency.value = next.concurrency ?? 2
  maxLatency.value = next.max_latency ?? ''
}

function sourceLabel(source?: ProxyPoolSettings['source']): string {
  return source === 'database' ? '数据库配置' : source === 'environment' ? '环境变量' : '未配置'
}

function poolBadge(): { variant: 'success' | 'warning' | 'muted'; label: string } {
  const status = statusData.value
  if (!status?.configured) return { variant: 'muted', label: '未配置' }
  if (status.healthy_size === 0) return { variant: 'warning', label: '暂无可用出口' }
  if (status.last_error_code) return { variant: 'warning', label: `采集异常（${status.last_error_code}）` }
  return { variant: 'success', label: '运行正常' }
}

async function save(): Promise<void> {
  const patch: ProxyPoolPatch = buildPatch()
  const upstream = upstreamURL.value.trim()
  if (upstream) patch.upstream_url = upstream
  await savePatch(patch)
}

function buildPatch(): ProxyPoolPatch {
  return {
    enabled: enabled.value,
    validation_url: (validationUrl.value ?? '').trim(),
    validation_status: validationStatus.value ?? 404,
    interval: (interval.value ?? '5s').trim(),
    proxy_ttl: (proxyTTL.value ?? '120s').trim(),
    expected_qty: expectedQty.value ?? 2,
    concurrency: concurrency.value ?? 2,
    max_latency: (maxLatency.value ?? '').trim(),
  }
}

async function clearUpstreamURL(): Promise<void> {
  if (!settings.value?.upstream_configured || saving.value) return
  if (enabled.value) {
    formError.value = '清除 XApi 地址前请先停用内置代理池。'
    return
  }
  await savePatch({ ...buildPatch(), upstream_url: '' })
}

async function savePatch(patch: ProxyPoolPatch): Promise<void> {
  if (disposed || saving.value) return
  controller?.abort()
  const next = new AbortController()
  controller = next
  saving.value = true
  formError.value = ''
  savedMessage.value = ''
  try {
    applySettings((await proxyPoolApi.update(patch, next.signal)).data)
    savedMessage.value = '配置已保存，新请求立即使用内置代理池。'
    await refreshStatus()
  } catch (error) {
    if (!disposed && !isAbortError(error)) {
      formError.value = error instanceof ApiError ? error.message : '代理池配置保存失败。'
    }
  } finally {
    if (!disposed && controller === next) saving.value = false
  }
}

async function refreshPool(): Promise<void> {
  if (disposed || refreshing.value) return
  refreshing.value = true
  formError.value = ''
  try {
    await proxyPoolApi.refresh()
    savedMessage.value = '已完成一轮采集与验证。'
    await refreshStatus()
  } catch (error) {
    formError.value = error instanceof ApiError ? error.message : '立即采集失败。'
  } finally {
    refreshing.value = false
  }
}
</script>

<template>
  <div class="page-container">
    <div class="content-wrapper">
      <UiPageHeader
        eyebrow="资源接入"
        title="代理池"
        subtitle="XApi 采集、出口验证、质量路由和 NVIDIA CONNECT 全部运行在本服务内。"
      >
        <template #actions>
          <UiBadge
            :variant="poolBadge().variant"
            :label="poolBadge().label"
          />
        </template>
      </UiPageHeader>

      <UiStatePanel
        :loading="loading"
        :error="errorMessage"
        loadingLabel="加载内置代理配置…"
        skeleton="text"
        :skeleton-lines="5"
        errorTestId="proxy-settings-load-error"
        retryTestId="proxy-settings-retry"
        retryLabel="重新加载"
        @retry="loadSettings"
      >
        <form
          class="card p-5 sm:p-6"
          novalidate
          @submit.prevent="save"
        >
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 class="type-heading">
                运行配置
              </h2>
              <p class="mt-1 text-sm text-[var(--color-text-muted)]">
                当前配置来源：{{ sourceLabel(settings?.source) }}。XApi 凭据只以密文保存，页面不回显完整地址。
              </p>
            </div>
            <label class="flex h-9 cursor-pointer items-center gap-2.5 text-sm text-[var(--color-text-secondary)]">
              <input
                v-model="enabled"
                data-testid="proxy-enabled"
                class="h-4 w-4 accent-[var(--color-accent)]"
                type="checkbox"
              >
              <span>启用内置代理池</span>
            </label>
          </div>

          <div class="mt-6 grid gap-x-5 gap-y-4 md:grid-cols-2">
            <div class="md:col-span-2">
              <span class="field-label">XApi 上游地址</span>
              <div
                data-testid="proxy-upstream-summary"
                class="panel-inset mt-1.5 px-3 py-2.5 font-mono-data text-sm text-[var(--color-text-secondary)]"
              >
                {{ settings?.upstream_configured ? `已配置（${settings.upstream_endpoint || 'endpoint 已脱敏'}）` : '未配置' }}
              </div>
              <input
                v-model="upstreamURL"
                data-testid="proxy-upstream-url"
                class="input-field mt-2 font-mono-data"
                type="password"
                autocomplete="new-password"
                placeholder="粘贴完整 XApi URL（保存后不回显）"
              >
              <span
                id="proxy-upstream-help"
                class="mt-1.5 block text-xs text-[var(--color-text-muted)]"
              >管理端可修改，完整地址只发送到后端加密保存；清除凭据前必须先停用代理池。</span>
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-validation-url-input"
              >验证地址</label>
              <input
                id="proxy-validation-url-input"
                v-model="validationUrl"
                data-testid="proxy-validation-url"
                class="input-field font-mono-data"
                type="url"
                placeholder="留空使用 NVIDIA API 根地址"
              >
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-validation-status-input"
              >预期状态码</label>
              <input
                id="proxy-validation-status-input"
                v-model.number="validationStatus"
                data-testid="proxy-validation-status"
                class="input-field"
                type="number"
                min="100"
                max="599"
              >
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-interval-input"
              >采集周期</label>
              <input
                id="proxy-interval-input"
                v-model="interval"
                data-testid="proxy-interval"
                class="input-field font-mono-data"
                placeholder="5s"
              >
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-ttl-input"
              >代理 TTL</label>
              <input
                id="proxy-ttl-input"
                v-model="proxyTTL"
                data-testid="proxy-ttl"
                class="input-field font-mono-data"
                placeholder="120s"
              >
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-concurrency-input"
              >采集并发</label>
              <input
                id="proxy-concurrency-input"
                v-model.number="concurrency"
                data-testid="proxy-concurrency"
                class="input-field"
                type="number"
                min="1"
                max="20"
              >
            </div>
            <div>
              <label
                class="field-label"
                for="proxy-max-latency-input"
              >最大延迟（可选）</label>
              <input
                id="proxy-max-latency-input"
                v-model="maxLatency"
                data-testid="proxy-max-latency"
                class="input-field font-mono-data"
                placeholder="例如 3s"
              >
            </div>
          </div>

          <p
            v-if="formError"
            class="mt-4 text-sm text-[var(--color-danger)]"
            role="alert"
          >
            {{ formError }}
          </p>
          <p
            v-if="savedMessage"
            class="badge-success mt-4 inline-flex px-3 py-1 text-sm"
          >
            {{ savedMessage }}
          </p>

          <div class="mt-6 flex flex-wrap justify-end gap-2 border-t border-[var(--color-border-subtle)] pt-5">
            <UiButton
              data-testid="proxy-refresh-now"
              variant="secondary"
              :loading="refreshing"
              loading-label="采集中…"
              :disabled="!settings?.enabled"
              @click="refreshPool"
            >
              立即采集
            </UiButton>
            <UiButton
              data-testid="proxy-clear-upstream"
              variant="secondary"
              :disabled="saving || !settings?.upstream_configured || enabled"
              @click="clearUpstreamURL"
            >
              清除 XApi 凭据
            </UiButton>
            <UiButton
              data-testid="proxy-save"
              variant="primary"
              type="submit"
              :loading="saving"
              loading-label="保存中…"
              :disabled="!settings"
            >
              保存配置
            </UiButton>
          </div>
        </form>
      </UiStatePanel>

      <section
        data-testid="proxy-status-panel"
        class="card mt-5"
        :aria-busy="statusLoading"
        aria-labelledby="proxy-status-heading"
      >
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-5 py-4">
          <div>
            <h2
              id="proxy-status-heading"
              class="type-heading"
            >
              实时池状态
            </h2>
            <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
              状态来自本进程内置 Collector，不依赖独立代理池服务。
            </p>
          </div>
          <UiButton
            variant="ghost"
            size="sm"
            icon="refresh"
            :disabled="statusLoading"
            @click="refreshStatus"
          >
            刷新
          </UiButton>
        </div>

        <div
          v-if="statusError"
          class="m-4 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ statusError }}
        </div>

        <div
          v-if="statusData"
          class="grid gap-3 p-5 sm:grid-cols-2 lg:grid-cols-4"
        >
          <UiStat
            label="有效出口"
            :value="statusData.healthy_size ?? 0"
          />
          <UiStat
            label="池内记录"
            :value="statusData.total_size ?? 0"
          />
          <UiStat
            label="最近采集"
            :value="formatLocalDateTime(statusData.last_success_at)"
          />
          <UiStat
            label="上游状态"
            :value="statusData.last_error_code || '正常'"
          />
        </div>

        <div
          v-if="statusData?.proxies?.length"
          class="overflow-x-auto border-t border-[var(--color-border)]"
        >
          <table class="data-table min-w-[760px]">
            <caption class="sr-only">
              内置代理出口质量
            </caption>
            <thead>
              <tr>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  出口
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  状态
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  延迟
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  质量分
                </th>
                <th
                  class="data-table-th"
                  scope="col"
                >
                  剩余 TTL
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="proxy in statusData.proxies"
                :key="proxy.address"
                class="data-table-row"
              >
                <td class="data-table-td font-mono-data">
                  {{ proxy.address }}
                </td>
                <td class="data-table-td">
                  <UiBadge
                    :variant="proxy.ejected ? 'danger' : proxy.healthy ? 'success' : 'muted'"
                    :label="proxy.healthy ? '健康' : proxy.ejected ? '隔离' : '待验证'"
                  />
                </td>
                <td class="data-table-td font-mono-data">
                  {{ proxy.latency_ewma_ms ?? '—' }} ms
                </td>
                <td class="data-table-td font-mono-data">
                  {{ proxy.quality_score ?? '—' }}
                </td>
                <td class="data-table-td font-mono-data">
                  {{ proxy.remaining_seconds ?? 0 }} s
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p
          v-else-if="statusData"
          class="p-5 text-sm text-[var(--color-text-muted)]"
        >
          暂无已验证出口。可点击“立即采集”重试。
        </p>

        <p
          v-if="statusUpdatedAt"
          class="border-t border-[var(--color-border)] px-5 py-3 text-xs text-[var(--color-text-subtle)]"
        >
          <span
            v-if="statusLoading"
            class="mr-2 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--color-info)]"
            aria-hidden="true"
          />更新于 {{ statusUpdatedAt.toLocaleTimeString() }}<span v-if="statusLoading">（刷新中…）</span>
        </p>
      </section>
    </div>
  </div>
</template>
