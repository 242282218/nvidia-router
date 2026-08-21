<script setup lang="ts">
import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

// 分段切换器：观测中心等「同页多视图」场景的统一控件。
// v-model 绑定当前 tab id；完整 tablist 语义 + 方向键导航。
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
    class="inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-[var(--shadow-xs)]"
    role="tablist"
    :aria-label="ariaLabel"
    @keydown="onKeydown"
  >
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="flex h-8 items-center gap-1.5 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color,box-shadow] duration-[var(--duration-micro)] pointer-coarse:h-11"
      :class="active === tab.id
        ? 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-[var(--shadow-xs)]'
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
