<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { ApiError, isAbortError } from '../../shared/api/client'
import { proxyPoolApi } from './api'
import type { ProxyPoolPatch, ProxyPoolSettings } from './types'

const settings = ref<ProxyPoolSettings | null>(null)
const enabled = ref(false)
const proxyUrl = ref('')
const authKey = ref('')
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const formError = ref('')
const savedMessage = ref('')
let disposed = false
let controller: globalThis.AbortController | null = null

onMounted(() => {
  void loadSettings()
})

onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
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

function sourceLabel(source?: ProxyPoolSettings['source']): string {
  if (source === 'database') return '数据库配置'
  if (source === 'environment') return '环境变量兜底'
  return '未配置'
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
    </div>
  </div>
</template>
