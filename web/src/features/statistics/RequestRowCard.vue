<script setup lang="ts">
import { computed, ref } from 'vue'

import UiBadge from '../../shared/ui/UiBadge.vue'
import UiIcon from '../../shared/ui/UiIcon.vue'
import { formatAverageLatency, formatLogDate, formatTokens } from './format'
import type { RequestLog } from './types'

// 请求明细行卡（设计 §5.5）：桌面为 12 列 CSS Grid 圆角行卡，移动端纵向堆叠。
// 次要字段（排队/思考/路由/请求 ID/上游 ID/错误码）收进行内展开区，
// 保证核心列在 88px 高度内的可读密度。
defineOptions({ name: 'RequestRowCard' })

const props = defineProps<{
  log: RequestLog
}>()

const expanded = ref(false)

const totalTokens = computed(() => (props.log.prompt_tokens ?? 0) + (props.log.completion_tokens ?? 0))

// 首字优先 first_token（流式语义），回退 first_byte（非流式测量点）。
const firstLatencyMs = computed(() => props.log.first_token_ms ?? props.log.first_byte_ms)

const outcomeLabel = computed(() => (props.log.outcome === 'success' ? '成功' : '失败'))

const streamLabel = computed(() => {
  if (!props.log.is_stream) return '非流式'
  return props.log.stream_done ? '流式 · 完成' : '流式 · 未完成'
})

const timeParts = computed(() => {
  const full = formatLogDate(props.log.created_at)
  // "2026/08/22 18:56:15" → 两行展示
  const spaceIndex = full.indexOf(' ')
  if (spaceIndex === -1) return { date: full, clock: '' }
  return { date: full.slice(0, spaceIndex), clock: full.slice(spaceIndex + 1) }
})
</script>

<template>
  <article
    class="card overflow-hidden"
    :class="expanded ? 'border-[var(--color-border-strong)]' : ''"
    data-testid="request-row-card"
  >
    <button
      type="button"
      class="grid w-full grid-cols-2 items-center gap-x-3 gap-y-2 p-3 text-left transition-colors duration-[var(--duration-micro)] hover:bg-[color-mix(in_srgb,var(--color-hover)_40%,transparent)] md:grid-cols-[minmax(150px,1.9fr)_minmax(110px,1.3fr)_minmax(78px,0.8fr)_minmax(92px,0.9fr)_minmax(92px,0.9fr)_minmax(52px,0.5fr)_minmax(96px,0.9fr)_28px] md:gap-x-2 md:p-0"
      :aria-expanded="expanded"
      :data-testid="`request-row-${log.request_id}`"
      @click="expanded = !expanded"
    >
      <!-- 密钥 / 模型 -->
      <div class="md:px-3 md:py-2.5">
        <p class="truncate font-mono-data text-xs font-medium text-[var(--color-text)]">
          {{ log.model_id ?? '未知模型' }}
        </p>
        <p class="mt-1 truncate text-xs text-[var(--color-text-muted)]">
          Access {{ log.access_key_id ?? '—' }} · NVIDIA {{ log.nvidia_key_id ?? '—' }}
        </p>
      </div>

      <!-- 端点 / 流式 -->
      <div class="md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <p class="truncate text-xs text-[var(--color-text-secondary)]">
          {{ log.endpoint }}
        </p>
        <p class="mt-1 text-xs text-[var(--color-text-subtle)]">
          {{ streamLabel }}
        </p>
      </div>

      <!-- 状态 -->
      <div class="flex items-center gap-1.5 md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <span
          class="h-2 w-2 shrink-0 rounded-full"
          :class="log.outcome === 'success' ? 'bg-[var(--color-success)]' : 'bg-[var(--color-danger)]'"
          aria-hidden="true"
        />
        <span
          class="text-xs"
          :class="log.outcome === 'success' ? 'text-[var(--color-success-text)]' : 'text-[var(--color-danger-text)]'"
        >{{ outcomeLabel }}</span>
        <span class="font-mono-data text-xs text-[var(--color-text-muted)]">{{ log.http_status }}</span>
      </div>

      <!-- 耗时：首字 / 总耗 -->
      <div class="md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <p class="font-mono-data text-xs text-[var(--color-text)]">
          <span class="text-[var(--color-text-subtle)]">总耗 </span>{{ formatAverageLatency(log.duration_ms) }}
        </p>
        <p class="mt-1 font-mono-data text-xs text-[var(--color-text-muted)]">
          <span class="text-[var(--color-text-subtle)]">首字 </span>{{ formatAverageLatency(firstLatencyMs) }}
        </p>
      </div>

      <!-- Token -->
      <div class="md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <p class="font-mono-data text-xs font-medium text-[var(--color-text)]">
          {{ totalTokens > 0 ? formatTokens(totalTokens) : '—' }}
        </p>
        <p
          v-if="totalTokens > 0"
          class="mt-1 font-mono-data text-xs text-[var(--color-text-muted)]"
        >
          ↑{{ formatTokens(log.prompt_tokens ?? 0) }} ↓{{ formatTokens(log.completion_tokens ?? 0) }}
        </p>
        <p
          v-else
          class="mt-1 text-xs text-[var(--color-text-subtle)]"
        >
          无用量
        </p>
      </div>

      <!-- 重试 -->
      <div class="md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <UiBadge
          v-if="log.attempt_count > 1"
          variant="warning"
          :label="`${log.attempt_count} 次`"
        />
        <span
          v-else
          class="font-mono-data text-xs text-[var(--color-text-muted)]"
        >1</span>
      </div>

      <!-- 时间 -->
      <div class="md:border-l md:border-[var(--color-border-subtle)] md:px-3 md:py-2.5">
        <p class="font-mono-data text-xs text-[var(--color-text-secondary)]">
          {{ timeParts.date }}
        </p>
        <p class="mt-1 font-mono-data text-xs text-[var(--color-text-muted)]">
          {{ timeParts.clock }}
        </p>
      </div>

      <!-- 展开指示 -->
      <span
        class="hidden items-center justify-center text-[var(--color-text-subtle)] md:flex"
        aria-hidden="true"
      >
        <UiIcon
          name="chevron-down"
          :size="14"
          :style="{ transform: expanded ? 'rotate(180deg)' : undefined }"
          class="transition-transform duration-[var(--duration-micro)]"
        />
      </span>
    </button>

    <!-- 展开详情：次要字段全量收纳 -->
    <div
      v-if="expanded"
      class="border-t border-[var(--color-border-subtle)] bg-[var(--color-sunken)] px-4 py-3"
      data-testid="request-row-detail"
    >
      <dl class="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2 lg:grid-cols-3">
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            请求 ID
          </dt>
          <dd class="truncate font-mono-data text-[var(--color-text-secondary)]">
            {{ log.request_id }}
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            排队等待
          </dt>
          <dd class="font-mono-data text-[var(--color-text-secondary)]">
            {{ formatAverageLatency(log.queue_ms) }}
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            首字节
          </dt>
          <dd class="font-mono-data text-[var(--color-text-secondary)]">
            {{ formatAverageLatency(log.first_byte_ms) }}
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            思考参数
          </dt>
          <dd class="truncate text-[var(--color-text-secondary)]">
            请求 {{ log.reasoning_requested ? '是' : '否' }} · 响应 {{ log.reasoning_present ? '是' : '否' }} · {{ log.reasoning_chars ?? '—' }} 字
            <template v-if="log.reasoning_wire_fields">
              （{{ log.reasoning_wire_fields }}）
            </template>
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            路由模式
          </dt>
          <dd class="truncate font-mono-data text-[var(--color-text-secondary)]">
            {{ log.route_mode ?? '—' }}
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3">
          <dt class="text-[var(--color-text-muted)]">
            错误码
          </dt>
          <dd class="truncate font-mono-data text-[var(--color-danger-text)]">
            {{ log.error_code ?? '—' }}
          </dd>
        </div>
        <div class="flex items-baseline justify-between gap-3 sm:col-span-2 lg:col-span-3">
          <dt class="text-[var(--color-text-muted)]">
            上游请求 ID
          </dt>
          <dd class="truncate font-mono-data text-[var(--color-text-secondary)]">
            {{ log.upstream_request_id ?? '—' }}
          </dd>
        </div>
      </dl>
    </div>
  </article>
</template>
