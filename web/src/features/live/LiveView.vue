<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { isFiniteNumber, isRecord } from '../../shared/api/client'
import type { LiveRequestEvent } from './types'

const events = ref<LiveRequestEvent[]>([])
const connected = ref(false)
const errorMessage = ref('')
const maxEvents = 500

let source: globalThis.EventSource | null = null

// The feed renders newest-first at the top. Auto-scroll keeps the newest event
// visible only while the operator stays pinned near the top; scrolling down to
// read history suspends it so a new event does not yank the viewport.
const listEl = ref<globalThis.HTMLElement | null>(null)
const pinnedToTop = ref(true)

watch(() => events.value.length, () => {
  if (pinnedToTop.value) {
    void nextTick(() => listEl.value?.scrollTo({ top: 0 }))
  }
})

function onListScroll(): void {
  const element = listEl.value
  if (!element) return
  pinnedToTop.value = element.scrollTop <= 8
}

function scrollToTop(): void {
  pinnedToTop.value = true
  // JS smooth scrolling is invisible to the theme.css reduced-motion guard
  // (it only neutralises CSS animations/transitions), so honour the preference
  // here directly (design-aesthetics 交互动效 P0#6).
  const reduced = typeof globalThis.matchMedia === 'function' && globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches
  listEl.value?.scrollTo({ top: 0, behavior: reduced ? 'auto' : 'smooth' })
}

function clearEvents(): void {
  events.value = []
}

onMounted(() => {
  connect()
})

onBeforeUnmount(() => {
  close()
})

function connect(): void {
  close()
  events.value = []
  errorMessage.value = ''
  try {
    // Same-origin EventSource carries the admin session cookie automatically.
    source = new globalThis.EventSource('/admin/api/events/stream')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '无法建立实时连接。'
    return
  }
  source.onopen = () => {
    connected.value = true
    errorMessage.value = ''
  }
  source.onerror = () => {
    // EventSource auto-reconnects on hiccups; only surface a terminal-looking
    // state once the browser gives up (readyState CLOSED).
    connected.value = false
    if (source?.readyState === globalThis.EventSource.CLOSED) {
      errorMessage.value = '实时连接已断开，请刷新重试。'
    }
  }
  source.addEventListener('request', (event) => {
    const parsed = parseRequestEvent((event as globalThis.MessageEvent).data)
    if (!parsed) return
    events.value.push(parsed)
    if (events.value.length > maxEvents) {
      events.value.splice(0, events.value.length - maxEvents)
    }
  })
}

function close(): void {
  source?.close()
  source = null
  connected.value = false
}

function parseRequestEvent(raw: string): LiveRequestEvent | null {
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isRecord(value)
    || typeof value.request_id !== 'string'
    || typeof value.endpoint !== 'string'
    || !isFiniteNumber(value.http_status)
    || typeof value.outcome !== 'string') return null
  return {
    request_id: value.request_id,
    endpoint: value.endpoint,
    model_id: typeof value.model_id === 'string' ? value.model_id : undefined,
    access_key_id: isFiniteNumber(value.access_key_id) ? value.access_key_id : undefined,
    nvidia_key_id: isFiniteNumber(value.nvidia_key_id) ? value.nvidia_key_id : undefined,
    http_status: value.http_status,
    outcome: value.outcome === 'success' ? 'success' : 'failure',
    error_code: typeof value.error_code === 'string' ? value.error_code : undefined,
    is_stream: value.is_stream === true,
    queue_ms: isFiniteNumber(value.queue_ms) ? value.queue_ms : 0,
    first_byte_ms: isFiniteNumber(value.first_byte_ms) ? value.first_byte_ms : undefined,
    first_token_ms: isFiniteNumber(value.first_token_ms) ? value.first_token_ms : undefined,
    duration_ms: isFiniteNumber(value.duration_ms) ? value.duration_ms : 0,
    created_at: typeof value.created_at === 'string' ? value.created_at : '',
  }
}

function statusBadge(status: number): string {
  if (status >= 200 && status < 300) return 'badge-success'
  if (status >= 400 && status < 500) return 'badge-warning'
  return 'badge-danger'
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatLatency(value: number | undefined): string {
  return value === undefined ? '—' : `${value} ms`
}

function latencyColor(duration: number): string {
  if (duration < 1000) return 'text-[var(--color-success)]'
  if (duration < 5000) return 'text-[var(--color-warning)]'
  return 'text-[var(--color-danger)]'
}
</script>

<template>
  <div class="page-container animate-fade-in">
    <div class="content-wrapper">
      <header class="section-header">
        <div>
          <p class="text-xs font-medium uppercase tracking-wider text-[var(--color-info)]">
            请求观测
          </p>
          <h1 class="page-title mt-1">
            实时请求流
          </h1>
          <p class="page-subtitle">
            通过 SSE 推送实时展示路由请求的元数据；仅保留最近 {{ maxEvents }} 条，最新在上、自动滚动。
          </p>
        </div>
        <div class="flex items-center gap-3">
          <span
            class="inline-flex items-center gap-2 text-sm"
            :class="connected ? 'text-[var(--color-success)]' : 'text-[var(--color-warning)]'"
          >
            <span
              class="h-2 w-2 rounded-full"
              :class="connected ? 'bg-[var(--color-success)] pulse-dot' : 'bg-[var(--color-warning)]'"
            />
            {{ connected ? '已连接' : '连接中…' }}
          </span>
          <button
            class="btn-ghost rounded-lg px-3 py-1.5 text-sm"
            type="button"
            :disabled="events.length === 0"
            @click="clearEvents"
          >
            清空
          </button>
          <button
            class="btn-ghost rounded-lg px-3 py-1.5 text-sm"
            type="button"
            @click="connect"
          >
            重连
          </button>
        </div>
      </header>

      <div
        v-if="errorMessage"
        class="mt-4 rounded-lg border border-[var(--color-danger)]/25 bg-[var(--color-danger)]/10 px-4 py-3 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ errorMessage }}
      </div>

      <div class="card mt-5 overflow-hidden">
        <div
          v-if="events.length === 0 && !errorMessage"
          class="p-8 text-center text-sm text-[var(--color-text-muted)]"
        >
          等待请求到达…
        </div>

        <div class="relative">
          <ul
            ref="listEl"
            class="max-h-[calc(100vh-16rem)] divide-y divide-[var(--color-border)] overflow-y-auto"
            @scroll="onListScroll"
          >
            <li
              v-for="event in [...events].reverse()"
              :key="event.request_id"
              class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 text-sm hover:bg-[var(--color-hover)]"
            >
              <span class="font-mono text-xs text-[var(--color-text-muted)]">
                {{ formatTime(event.created_at) }}
              </span>
              <code class="truncate font-mono text-xs text-[var(--color-info)]">{{ event.model_id || '—' }}</code>
              <code class="truncate font-mono text-xs text-[var(--color-text-secondary)]">{{ event.endpoint }}</code>
              <span
                class="rounded px-2 py-0.5 text-xs font-medium"
                :class="statusBadge(event.http_status)"
              >{{ event.http_status }}</span>
              <span
                v-if="event.error_code"
                class="truncate text-xs text-[var(--color-warning)]"
              >
                {{ event.error_code }}
              </span>
              <span
                class="ml-auto font-mono text-xs"
                :class="latencyColor(event.duration_ms)"
              >
                {{ formatLatency(event.duration_ms) }}
                <template v-if="event.first_token_ms !== undefined">· TTFT {{ formatLatency(event.first_token_ms) }}</template>
              </span>
            </li>
          </ul>
          <Transition name="fade">
            <button
              v-if="!pinnedToTop && events.length > 0"
              class="absolute right-4 bottom-4 rounded-full border border-[var(--color-border)] bg-[var(--color-elevated)] px-3 py-1.5 text-xs text-[var(--color-text-secondary)] shadow-[var(--shadow-overlay)] transition-colors hover:text-[var(--color-text)]"
              type="button"
              @click="scrollToTop"
            >
              回到最新
            </button>
          </Transition>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.fade-enter-active {
  transition: opacity 0.18s cubic-bezier(0.0, 0.0, 0.2, 1);
}
.fade-leave-active {
  transition: opacity 0.13s cubic-bezier(0.4, 0.0, 1, 1);
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
