<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'

import { isFiniteNumber, isRecord } from '../../shared/api/client'
import { formatClock, formatLatency } from '../../shared/format'
import UiBadge from '../../shared/ui/UiBadge.vue'
import UiButton from '../../shared/ui/UiButton.vue'
import UiEmptyState from '../../shared/ui/UiEmptyState.vue'
import UiPageHeader from '../../shared/ui/UiPageHeader.vue'
import type { LiveRequestEvent } from './types'

withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })

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

// Inside the observability KeepAlive, switching tabs deactivates this pane
// instead of unmounting it — an invisible SSE stream would keep receiving
// events and re-rendering a hidden list. Close on deactivate; reconnect (and
// clear stale buffered events) on activate. Outside KeepAlive these hooks
// never fire and behaviour is unchanged.
onDeactivated(() => {
  close()
})

onActivated(() => {
  if (!source) connect()
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

// Newest-first view of the event feed. Computed so the reversed array is
// cached per render instead of being rebuilt on every list update.
const reversedEvents = computed(() => [...events.value].reverse())

function statusBadge(status: number): { variant: 'success' | 'warning' | 'danger'; label: string } {
  if (status >= 200 && status < 300) return { variant: 'success', label: String(status) }
  if (status >= 400 && status < 500) return { variant: 'warning', label: String(status) }
  return { variant: 'danger', label: String(status) }
}

function latencyColor(duration: number): string {
  if (duration < 1000) return 'text-[var(--color-success)]'
  if (duration < 5000) return 'text-[var(--color-warning)]'
  return 'text-[var(--color-danger)]'
}
</script>

<template>
  <div :class="embedded ? '' : 'page-container'">
    <div :class="embedded ? '' : 'content-wrapper'">
      <UiPageHeader
        v-if="!embedded"
        eyebrow="系统观测"
        title="实时请求流"
        :subtitle="`通过 SSE 推送实时展示路由请求的元数据；仅保留最近 ${maxEvents} 条，最新在上、自动滚动。`"
      >
        <template #actions>
          <span
            class="badge flex items-center gap-1.5"
            :class="connected ? 'badge-success' : 'badge-warning'"
            data-testid="live-connection"
          >
            <span
              :class="connected ? 'pulse-dot bg-[var(--color-success)]' : ''"
              class="h-1.5 w-1.5 rounded-full"
              aria-hidden="true"
            />
            {{ connected ? '已连接' : '连接中…' }}
          </span>
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="events.length === 0"
            @click="clearEvents"
          >
            清空
          </UiButton>
          <UiButton
            variant="secondary"
            size="sm"
            icon="refresh"
            @click="connect"
          >
            重连
          </UiButton>
        </template>
      </UiPageHeader>

      <!-- Embedded toolbar -->
      <div
        v-if="embedded"
        class="mb-3 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border-subtle)] pb-3"
      >
        <div class="flex items-center gap-2">
          <UiBadge
            :variant="connected ? 'success' : 'warning'"
            :label="connected ? '实时接收中' : '连接中…'"
          />
          <span class="text-xs text-[var(--color-text-subtle)]">已缓冲 {{ events.length }} / {{ maxEvents }} 条</span>
        </div>
        <div class="flex items-center gap-2">
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="events.length === 0"
            @click="clearEvents"
          >
            清空
          </UiButton>
          <UiButton
            variant="secondary"
            size="sm"
            icon="refresh"
            @click="connect"
          >
            重连
          </UiButton>
        </div>
      </div>

      <div
        v-if="errorMessage"
        class="mt-4 rounded-[var(--radius-control)] border border-[color-mix(in_srgb,var(--color-danger)_25%,transparent)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] px-4 py-3 text-sm text-[var(--color-danger)]"
        role="alert"
      >
        {{ errorMessage }}
      </div>

      <div class="card mt-4 overflow-hidden">
        <UiEmptyState
          v-if="events.length === 0 && !errorMessage"
          icon="bolt"
          title="等待请求到达…"
          hint="实时事件流已就绪，新请求元数据会出现在这里。"
        />

        <div class="relative">
          <div
            ref="listEl"
            class="max-h-[calc(100vh-16rem)] overflow-y-auto"
            @scroll="onListScroll"
          >
            <TransitionGroup
              tag="ul"
              name="feed"
              class="divide-y divide-[var(--color-border-subtle)]"
            >
              <li
                v-for="event in reversedEvents"
                :key="event.request_id"
                class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 text-sm transition-colors hover:bg-[var(--color-hover)] sm:grid sm:grid-cols-[4.5rem_minmax(0,1.4fr)_minmax(0,1.6fr)_5rem_minmax(0,1fr)_auto] sm:gap-x-3"
              >
                <span class="font-mono-data text-xs text-[var(--color-text-muted)]">
                  {{ formatClock(event.created_at) }}
                </span>
                <code class="truncate font-mono-data text-xs text-[var(--color-info)]">{{ event.model_id || '—' }}</code>
                <code class="truncate font-mono-data text-xs text-[var(--color-text-secondary)]">{{ event.endpoint }}</code>
                <UiBadge
                  class="justify-self-start"
                  :variant="statusBadge(event.http_status).variant"
                  :label="statusBadge(event.http_status).label"
                  :dot="false"
                />
                <!-- 始终占位：错误码缺失时若整格不渲染，后面的耗时会滑进本列，
                     行与行之间的列就对不齐了。 -->
                <span
                  class="truncate text-xs text-[var(--color-warning)]"
                  :class="event.error_code ? '' : 'hidden sm:block'"
                >
                  {{ event.error_code }}
                </span>
                <span
                  class="ml-auto font-mono-data text-xs sm:ml-0 sm:text-right"
                  :class="latencyColor(event.duration_ms)"
                >
                  {{ formatLatency(event.duration_ms) }}
                  <template v-if="event.first_token_ms !== undefined">· TTFT {{ formatLatency(event.first_token_ms) }}</template>
                </span>
              </li>
            </TransitionGroup>
          </div>
          <Transition name="fade">
            <button
              v-if="!pinnedToTop && events.length > 0"
              class="absolute bottom-4 right-4 rounded-full border border-[var(--color-border)] bg-[var(--color-elevated)] px-3 py-1.5 text-xs text-[var(--color-text-secondary)] transition-colors hover:text-[var(--color-text)] pointer-coarse:px-4 pointer-coarse:py-2.5"
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

/* 实时流入场：新事件从上方轻滑落（最新在上），旧事件淡出让位 */
.feed-enter-active {
  transition: opacity var(--duration-local) var(--ease-enter), transform var(--duration-local) var(--ease-enter);
}
.feed-enter-from {
  opacity: 0;
  transform: translateY(-10px);
}
.feed-leave-active {
  transition: opacity var(--duration-micro) var(--ease-exit);
}
.feed-leave-to {
  opacity: 0;
}
</style>
