<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError, isFiniteNumber, isRecord } from '../../shared/api/client'
import { proxyPoolApi } from './api'
import type { PoolProxyStatus, PoolStatusData, ProxyPoolPatch, ProxyPoolSettings } from './types'

const settings = ref<ProxyPoolSettings | null>(null)
const enabled = ref(false)
const proxyUrl = ref('')
const authKey = ref('')
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const formError = ref('')
const savedMessage = ref('')
const statusData = ref<PoolStatusData | null>(null)
const statusLoading = ref(false)
const statusError = ref('')
// statusUpdatedAt records when the last status poll landed, so the operator can
// judge how stale the displayed quality numbers are between the 10s polls.
const statusUpdatedAt = ref<Date | null>(null)
let disposed = false
let controller: globalThis.AbortController | null = null
let statusController: globalThis.AbortController | null = null
let statusTimer: ReturnType<typeof globalThis.setInterval> | undefined

onMounted(() => {
  void loadSettings()
  void refreshStatus()
  statusTimer = globalThis.setInterval(() => void refreshStatus(), 10_000)
})

onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  statusController?.abort()
  if (statusTimer !== undefined) globalThis.clearInterval(statusTimer)
})

async function loadSettings(): Promise<void> {
  controller?.abort()
  const nextController = new globalThis.AbortController()
  controller = nextController
  loading.value = true
  try {
    applySettings((await proxyPoolApi.get(nextController.signal)).data)
    errorMessage.value = ''
  } catch (error) {
    if (!disposed && !isAbortError(error)) {
      errorMessage.value = error instanceof ApiError ? error.message : '代理池配置加载失败。'
    }
  } finally {
    if (!disposed && controller === nextController) loading.value = false
  }
}

async function refreshStatus(): Promise<void> {
  if (disposed) return
  statusController?.abort()
  const nextController = new globalThis.AbortController()
  statusController = nextController
  if (statusData.value === null) statusLoading.value = true
  try {
    const response: unknown = await proxyPoolApi.status(nextController.signal)
    if (disposed || statusController !== nextController) return
    if (!isPoolStatusData(response)) throw new TypeError('Invalid proxy pool status response.')
    statusData.value = response.data
    statusUpdatedAt.value = new Date()
    statusError.value = ''
  } catch (error) {
    if (disposed || statusController !== nextController || isAbortError(error)) return
    statusError.value = error instanceof ApiError ? error.message : '代理池状态加载失败。'
  } finally {
    if (!disposed && statusController === nextController) statusLoading.value = false
  }
}

function isPoolStatusData(value: unknown): value is { data: PoolStatusData } {
  if (!isRecord(value) || !isRecord(value.data)) return false
  const data = value.data
  return isFiniteNumber(data.total_size)
    && isFiniteNumber(data.healthy_size)
    && Array.isArray(data.proxies)
    && data.proxies.every(isPoolProxyStatus)
}

function isPoolProxyStatus(value: unknown): value is PoolProxyStatus {
  if (!isRecord(value) || typeof value.address !== 'string') return false
  return typeof value.healthy === 'boolean'
    && typeof value.ejected === 'boolean'
    && isFiniteNumber(value.latency_ewma_ms)
    && isFiniteNumber(value.remaining_seconds)
    && isFiniteNumber(value.success_count)
    && isFiniteNumber(value.failure_count)
    && isFiniteNumber(value.http_fail_count)
}

function formatLatency(ms: number): string {
  return `${ms} ms`
}

function formatRemaining(seconds: number): string {
  if (seconds < 0) return '—'
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m ${seconds % 60}s`
}

function formatClock(value: Date): string {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(value.getHours())}:${pad(value.getMinutes())}:${pad(value.getSeconds())}`
}

function sourceLabel(source?: ProxyPoolSettings['source']): string {
  if (source === 'database') return '数据库配置'
  if (source === 'environment') return '环境变量兜底'
  return '未配置'
}

function applySettings(next: ProxyPoolSettings): void {
  settings.value = next
  enabled.value = next.enabled
  proxyUrl.value = next.proxy_url
  authKey.value = ''
}

async function save(): Promise<void> {
  await savePatch({ enabled: enabled.value, proxy_url: proxyUrl.value.trim(), auth_key: authKey.value })
}

async function clearAuthKey(): Promise<void> {
  await savePatch({
    enabled: false,
    proxy_url: proxyUrl.value.trim(),
    auth_key: '',
    clear_auth_key: true,
  })
}

async function savePatch(patch: ProxyPoolPatch): Promise<void> {
  if (disposed || saving.value) return
  if (patch.enabled && !isValidProxyURL(patch.proxy_url)) {
    formError.value = '启用代理池时请输入有效的 HTTP 或 HTTPS 代理地址。'
    return
  }
  controller?.abort()
  const nextController = new globalThis.AbortController()
  controller = nextController
  saving.value = true
  formError.value = ''
  savedMessage.value = ''
  try {
    applySettings((await proxyPoolApi.update(patch, nextController.signal)).data)
    savedMessage.value = patch.clear_auth_key ? '认证 Key 已清除。' : '配置已保存，新请求立即使用此配置。'
  } catch (error) {
    if (!disposed && !isAbortError(error)) {
      formError.value = error instanceof ApiError ? error.message : '代理池配置保存失败。'
    }
  } finally {
    if (!disposed && controller === nextController) saving.value = false
  }
}

function isValidProxyURL(raw: string): boolean {
  try {
    const parsed = new globalThis.URL(raw)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:')
      && parsed.hostname !== ''
      && parsed.username === ''
      && parsed.password === ''
      && parsed.search === ''
      && parsed.hash === ''
      && (parsed.pathname === '' || parsed.pathname === '/')
  } catch {
    return false
  }
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <header class="section-header">
        <div>
          <p class="text-xs font-medium uppercase tracking-wider text-[var(--color-accent-bright)]">
            出口连接
          </p>
          <h1 class="page-title mt-1">
            代理池
          </h1>
          <p class="page-subtitle">
            管理星空代理池的标准 HTTP 正向代理连接。认证 Key 只显示配置状态，不会回显。
          </p>
        </div>
      </header>

      <p
        v-if="errorMessage"
        class="mb-4 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ errorMessage }}
      </p>

      <div
        v-if="loading"
        class="card flex items-center gap-3 p-6 text-sm text-[var(--color-text-muted)]"
      >
        <span class="h-4 w-4 animate-spin rounded-full border-2 border-[var(--color-border-strong)] border-t-[var(--color-accent)]" />
        加载中…
      </div>

      <form
        v-else
        class="card max-w-3xl p-5 animate-slide-up"
        novalidate
        @submit.prevent="save"
      >
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 class="text-sm font-medium text-[var(--color-text)]">
              代理池连接
            </h2>
            <p class="mt-1 text-sm text-[var(--color-text-muted)]">
              保存后，新的 NVIDIA 请求会立即采用当前配置；正在进行的请求保持原连接。
            </p>
          </div>
          <span
            data-testid="proxy-source"
            class="badge-muted"
          >{{ sourceLabel(settings?.source) }}</span>
        </div>

        <label class="mt-6 flex cursor-pointer items-center gap-3 text-sm font-medium text-[var(--color-text-secondary)]">
          <input
            v-model="enabled"
            data-testid="proxy-enabled"
            class="h-4 w-4 accent-[var(--color-accent)]"
            type="checkbox"
          >
          <span>启用代理池</span>
        </label>

        <div class="mt-5 grid gap-5 sm:grid-cols-2">
          <label class="block text-sm font-medium text-[var(--color-text-secondary)] sm:col-span-2">
            <span>代理地址</span>
            <input
              v-model="proxyUrl"
              data-testid="proxy-url"
              class="input-field mt-1.5 font-mono"
              type="url"
              inputmode="url"
              autocomplete="url"
              placeholder="http://proxy-pool:8080"
              :aria-invalid="Boolean(formError)"
            >
            <span class="mt-1 block text-xs text-[var(--color-text-muted)]">仅支持 HTTP/HTTPS、主机和端口，不要填写用户名、密码、路径或查询参数。</span>
          </label>

          <label class="block text-sm font-medium text-[var(--color-text-secondary)]">
            <span>认证 Key</span>
            <input
              v-model="authKey"
              data-testid="proxy-auth-key"
              class="input-field mt-1.5 font-mono"
              type="password"
              autocomplete="new-password"
              placeholder="留空保持现有 Key"
            >
            <span class="mt-1 block text-xs text-[var(--color-text-muted)]">提交后不会再次显示明文。</span>
          </label>

          <div class="text-sm text-[var(--color-text-secondary)]">
            <span class="block font-medium">认证状态</span>
            <span
              data-testid="proxy-auth-status"
              class="mt-2 inline-flex"
              :class="settings?.auth_configured ? 'badge-success' : 'badge-muted'"
            >{{ settings?.auth_configured ? '已配置' : '未配置' }}</span>
          </div>
        </div>

        <p
          v-if="formError"
          data-testid="proxy-pool-error"
          class="mt-4 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ formError }}
        </p>
        <p
          v-if="savedMessage"
          data-testid="proxy-pool-saved"
          class="mt-4 text-sm badge-success inline-flex px-3 py-1"
        >
          {{ savedMessage }}
        </p>

        <div class="mt-6 flex flex-wrap justify-end gap-3">
          <button
            data-testid="proxy-clear-auth"
            class="btn-secondary rounded-lg px-4 py-2.5 text-sm"
            type="button"
            :disabled="saving || !settings?.auth_configured"
            @click="clearAuthKey"
          >
            清除认证 Key
          </button>
          <button
            data-testid="proxy-save"
            class="btn-primary rounded-lg px-5 py-2.5 text-sm"
            type="submit"
            :disabled="saving || !settings"
          >
            {{ saving ? '保存中…' : '保存配置' }}
          </button>
        </div>
      </form>

      <section
        data-testid="proxy-status-panel"
        class="card mt-4 animate-slide-up"
        aria-labelledby="proxy-status-heading"
      >
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-5 py-4">
          <div>
            <h2
              id="proxy-status-heading"
              class="text-sm font-medium text-[var(--color-text)]"
            >
              代理池状态
            </h2>
            <p class="mt-1 text-xs text-[var(--color-text-muted)]">
              内置池实时质量：健康代理数、延迟与剩余寿命。静态代理模式不采集。
            </p>
          </div>
          <div class="flex items-center gap-3">
            <span
              v-if="statusData"
              class="text-xs text-[var(--color-text-muted)]"
            >
              健康 <span class="font-mono font-semibold text-[var(--color-success)]">{{ statusData.healthy_size }}</span> / {{ statusData.total_size }}
            </span>
            <span
              v-if="statusUpdatedAt"
              class="hidden text-xs text-[var(--color-text-subtle)] sm:inline"
              :title="`最后刷新于 ${statusUpdatedAt.toLocaleString()}`"
            >
              更新于 {{ formatClock(statusUpdatedAt) }}
            </span>
            <button
              class="btn-ghost"
              type="button"
              :disabled="statusLoading"
              aria-label="刷新代理池状态"
              @click="refreshStatus"
            >
              {{ statusLoading && statusData === null ? '加载中…' : '刷新' }}
            </button>
          </div>
        </div>

        <p
          v-if="statusError"
          class="m-4 rounded-lg border border-[#ef4444]/25 bg-[#ef4444]/10 px-4 py-3 text-sm text-[var(--color-danger)]"
          role="alert"
        >
          {{ statusError }}
        </p>
        <p
          v-else-if="statusLoading && statusData === null"
          class="flex items-center gap-3 p-6 text-sm text-[var(--color-text-muted)]"
        >
          <span class="h-4 w-4 animate-spin rounded-full border-2 border-[var(--color-border-strong)] border-t-[var(--color-accent)]" />
          加载代理池状态…
        </p>
        <template v-else-if="statusData">
          <div
            v-if="statusData.proxies.length === 0"
            class="p-6 text-center text-sm text-[var(--color-text-muted)]"
          >
            <p>{{ statusData.total_size === 0 ? '当前无动态代理：静态代理模式不采集，或上游采集尚未返回有效代理。' : '所有代理均在隔离或冷却中，等待下一次采集恢复。' }}</p>
            <p class="mt-1 text-xs text-[var(--color-text-subtle)]">
              内置池每 5 秒向上游采集并校验一次；若长时间为空，请检查上游地址与配额。
            </p>
          </div>
          <ul
            v-else
            class="divide-y divide-[var(--color-border)]"
          >
            <li
              v-for="proxy in statusData.proxies"
              :key="proxy.address"
              class="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3"
            >
              <span class="min-w-0 flex-1 font-mono text-sm text-[var(--color-text)]">
                {{ proxy.address }}
              </span>
              <span
                :class="proxy.healthy ? 'badge-success' : (proxy.ejected ? 'badge-danger' : 'badge-warning')"
              >
                {{ proxy.healthy ? '健康' : (proxy.ejected ? '隔离' : '待检') }}
              </span>
              <span class="text-xs text-[var(--color-text-muted)]">
                延迟 <span class="font-mono text-[var(--color-text-secondary)]">{{ formatLatency(proxy.latency_ewma_ms) }}</span>
              </span>
              <span class="text-xs text-[var(--color-text-muted)]">
                剩余 <span class="font-mono text-[var(--color-text-secondary)]">{{ formatRemaining(proxy.remaining_seconds) }}</span>
              </span>
              <span class="text-xs text-[var(--color-text-muted)]">
                成功 <span class="font-mono text-[var(--color-text-secondary)]">{{ proxy.success_count }}</span> · 失败 <span class="font-mono text-[var(--color-text-secondary)]">{{ proxy.failure_count }}</span>
              </span>
              <span
                v-if="proxy.http_fail_count > 0"
                class="badge-warning"
                :title="`连续 ${proxy.http_fail_count} 次 HTTP 失败（429/5xx），可能被上游限流`"
              >
                限流信号 ×{{ proxy.http_fail_count }}
              </span>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </div>
</template>
