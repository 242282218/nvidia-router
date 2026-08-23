<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

// 分段切换器：观测中心等「同页多视图」场景的统一控件。
// v-model 绑定当前 tab id；完整 tablist 语义 + 方向键导航。
// 激活态由单个滑动胶囊承载（与侧栏导航同一 FLIP 思想），避免逐按钮背景跳变。
defineOptions({ name: 'UiTabs' })

export interface UiTabItem {
  id: string
  label: string
  icon?: IconName
  testId?: string
}

const props = defineProps<{
  tabs: UiTabItem[]
  ariaLabel?: string
}>()

const active = defineModel<string>({ required: true })

const emit = defineEmits<{ change: [id: string] }>()

const listRef = ref<globalThis.HTMLElement | null>(null)
const indicator = ref({ left: 0, width: 0, visible: false })

function syncIndicator(): void {
  const container = listRef.value
  if (!container) return
  // tab id 为内部常量（runtime/statistics/live/audit），无需 CSS.escape
  const current = container.querySelector<globalThis.HTMLElement>(`[data-tab-id="${active.value}"]`)
  if (!current) {
    indicator.value = { left: 0, width: 0, visible: false }
    return
  }
  indicator.value = {
    left: current.offsetLeft,
    width: current.offsetWidth,
    visible: true,
  }
}

watch([active, () => props.tabs], () => {
  void nextTick(syncIndicator)
}, { immediate: true })

onMounted(() => {
  void nextTick(syncIndicator)
  globalThis.addEventListener('resize', syncIndicator)
})

onBeforeUnmount(() => {
  globalThis.removeEventListener('resize', syncIndicator)
})

function select(id: string): void {
  if (active.value === id) return
  active.value = id
  emit('change', id)
}

function onKeydown(event: globalThis.KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  const ids = props.tabs.map((tab) => tab.id)
  const index = ids.indexOf(active.value)
  if (index === -1) return
  const delta = event.key === 'ArrowRight' ? 1 : -1
  const next = ids[(index + delta + ids.length) % ids.length]
  if (next === undefined) return
  select(next)
  const group = event.currentTarget as globalThis.HTMLElement | null
  group?.querySelector<globalThis.HTMLButtonElement>(`[data-tab-id="${next}"]`)?.focus()
}
</script>

<template>
  <div
    class="relative inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1"
    role="tablist"
    :aria-label="ariaLabel"
    @keydown="onKeydown"
  >
    <!-- 滑动激活胶囊：跟随当前 tab 平移 -->
    <div
      v-if="indicator.visible"
      class="absolute top-1 bottom-1 rounded-[calc(var(--radius-control)-2px)] border border-[var(--color-border)] bg-[var(--color-elevated)] transition-[transform,width,opacity] duration-300 ease-[cubic-bezier(0.34,1.3,0.64,1)]"
      :style="{ transform: `translateX(${indicator.left}px)`, width: `${indicator.width}px` }"
      aria-hidden="true"
    />
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="relative z-10 flex h-8 items-center gap-1.5 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-colors duration-[var(--duration-micro)] pointer-coarse:h-11"
      :class="active === tab.id
        ? 'text-[var(--color-text)]'
        : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)]'"
      :data-testid="tab.testId"
      :data-tab-id="tab.id"
      type="button"
      role="tab"
      :aria-selected="active === tab.id"
      :tabindex="active === tab.id ? 0 : -1"
      @click="select(tab.id)"
    >
      <UiIcon
        v-if="tab.icon"
        :name="tab.icon"
        :size="14"
      />
      <span>{{ tab.label }}</span>
    </button>
  </div>
</template>
