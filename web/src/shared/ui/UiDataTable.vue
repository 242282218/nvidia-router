<script setup lang="ts" generic="T">
import { computed, ref } from 'vue'
import { AnimatePresence, Motion } from 'motion-v'

import UiIcon from './UiIcon.vue'
import type { DataTableColumn } from './dataTable'

// 数据表 v2：吸顶表头、列排序、行多选 + 浮动批量操作条、双密度。
// 单元格内容通过 cell-{key} 插槽注入，保持语义化 <table> 结构
// （分页由页面层负责，组件不做虚拟滚动——见设计文档决策记录）。
defineOptions({ name: 'UiDataTable' })

const props = withDefaults(defineProps<{
  columns: readonly DataTableColumn<T>[]
  rows: readonly T[]
  rowKey: (row: T) => string | number
  caption?: string
  selectable?: boolean
  loading?: boolean
  density?: 'comfortable' | 'compact'
  /** 表体最大高度（CSS 值）；提供时表头吸附。 */
  maxHeight?: string
  testId?: string
}>(), {
  caption: undefined,
  selectable: false,
  loading: false,
  density: 'comfortable',
  maxHeight: undefined,
  testId: undefined,
})

const emit = defineEmits<{
  selectionChange: [keys: (string | number)[]]
}>()

// ── 排序 ──
const sortKey = ref<string | null>(null)
const sortDir = ref<'asc' | 'desc'>('asc')

function sortIconName(key: string): 'sort' | 'arrow-up' | 'arrow-down' {
  if (sortKey.value !== key) return 'sort'
  return sortDir.value === 'asc' ? 'arrow-up' : 'arrow-down'
}

function toggleSort(column: DataTableColumn<T>): void {
  if (!column.sortable) return
  if (sortKey.value === column.key) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  }
  else {
    sortKey.value = column.key
    sortDir.value = 'asc'
  }
}

const sortedRows = computed(() => {
  const key = sortKey.value
  if (!key) return props.rows
  const column = props.columns.find((c) => c.key === key)
  const valueOf = column?.value
  if (!valueOf) return props.rows
  const factor = sortDir.value === 'asc' ? 1 : -1
  return [...props.rows].sort((a, b) => {
    const va = valueOf(a)
    const vb = valueOf(b)
    if (va < vb) return -1 * factor
    if (va > vb) return 1 * factor
    return 0
  })
})

// ── 选择 ──
const selected = ref(new Set<string | number>())

function keyOf(row: T): string | number {
  return props.rowKey(row)
}

function isSelected(row: T): boolean {
  return selected.value.has(keyOf(row))
}

function toggleRow(row: T): void {
  const next = new Set(selected.value)
  const k = keyOf(row)
  if (next.has(k)) next.delete(k)
  else next.add(k)
  selected.value = next
  emit('selectionChange', [...next])
}

const allSelected = computed(() =>
  props.rows.length > 0 && props.rows.every((row) => selected.value.has(keyOf(row))),
)

const someSelected = computed(() =>
  props.rows.some((row) => selected.value.has(keyOf(row))),
)

function toggleAll(): void {
  const next = new Set<string | number>()
  if (!allSelected.value) {
    for (const row of props.rows) next.add(keyOf(row))
  }
  selected.value = next
  emit('selectionChange', [...next])
}

const selectedCount = computed(() => selected.value.size)

function clearSelection(): void {
  selected.value = new Set()
  emit('selectionChange', [])
}

const padClass = computed(() => (props.density === 'compact'
  ? 'px-3 py-1.5'
  : 'px-4 py-3'))
</script>

<template>
  <div
    class="card overflow-hidden"
    :data-testid="testId"
  >
    <div
      class="relative overflow-auto focus-within:outline-2 focus-within:-outline-offset-2 focus-within:outline-[var(--color-focus)]"
      :style="maxHeight ? { maxHeight } : undefined"
      tabindex="0"
      role="region"
      :aria-label="caption ?? '数据表，可滚动'"
    >
      <table class="data-table">
        <caption
          v-if="caption"
          class="sr-only"
        >
          {{ caption }}
        </caption>
        <thead class="sticky top-0 z-10 bg-[var(--color-surface)]">
          <tr>
            <th
              v-if="selectable"
              class="data-table-th w-10"
              scope="col"
            >
              <label class="checkbox-hit">
                <input
                  type="checkbox"
                  class="checkbox-control"
                  :checked="allSelected"
                  :indeterminate="someSelected && !allSelected"
                  aria-label="全选"
                  @change="toggleAll"
                >
              </label>
            </th>
            <th
              v-for="column in columns"
              :key="column.key"
              class="data-table-th"
              :class="[column.align === 'right' ? 'text-right' : column.align === 'center' ? 'text-center' : '', column.width]"
              scope="col"
              :aria-sort="sortKey === column.key ? (sortDir === 'asc' ? 'ascending' : 'descending') : undefined"
            >
              <component
                :is="column.sortable ? 'button' : 'span'"
                :class="column.sortable ? 'group/sort inline-flex min-h-6 cursor-pointer items-center gap-1.5 transition-colors duration-[var(--duration-micro)] hover:text-[var(--color-text)] pointer-coarse:min-h-11' : ''"
                :type="column.sortable ? 'button' : undefined"
                @click="toggleSort(column)"
              >
                <span :class="sortKey === column.key ? 'text-[var(--color-text-secondary)]' : ''">{{ column.label }}</span>
                <UiIcon
                  v-if="column.sortable"
                  :name="sortIconName(column.key)"
                  :size="13"
                  class="shrink-0 transition-opacity duration-[var(--duration-micro)]"
                  :class="sortKey === column.key
                    ? 'text-[var(--color-text)]'
                    : 'text-[var(--color-text-subtle)] opacity-55 group-hover/sort:opacity-100'"
                />
              </component>
            </th>
          </tr>
        </thead>

        <tbody v-if="loading">
          <tr
            v-for="i in 5"
            :key="`skeleton-${i}`"
            aria-hidden="true"
          >
            <td
              v-for="(column, ci) in columns"
              :key="column.key"
              class="data-table-td"
              :class="padClass"
            >
              <div
                class="skeleton h-4"
                :style="{ width: `${72 - ((ci * 13 + i * 7) % 40)}%` }"
              />
            </td>
          </tr>
        </tbody>

        <tbody v-else-if="sortedRows.length === 0">
          <tr>
            <td
              :colspan="columns.length + (selectable ? 1 : 0)"
              class="data-table-td"
            >
              <slot name="empty">
                <div class="flex flex-col items-center gap-2 py-10 text-sm text-[var(--color-text-muted)]">
                  <UiIcon
                    name="inbox"
                    :size="28"
                    class="text-[var(--color-text-subtle)]"
                  />
                  暂无数据
                </div>
              </slot>
            </td>
          </tr>
        </tbody>

        <tbody v-else>
          <tr
            v-for="(row, index) in sortedRows"
            :key="keyOf(row)"
            class="data-table-row"
            :class="isSelected(row) ? 'bg-[var(--color-hover)]' : ''"
          >
            <td
              v-if="selectable"
              class="data-table-td w-10"
              :class="padClass"
            >
              <label class="checkbox-hit">
                <input
                  type="checkbox"
                  class="checkbox-control"
                  :checked="isSelected(row)"
                  :aria-label="`选择第 ${index + 1} 行`"
                  @change="toggleRow(row)"
                >
              </label>
            </td>
            <td
              v-for="column in columns"
              :key="column.key"
              class="data-table-td"
              :class="[padClass, column.align === 'right' ? 'text-right' : column.align === 'center' ? 'text-center' : '']"
            >
              <slot
                :name="`cell-${column.key}`"
                :row="row"
                :index="index"
              >
                {{ String((row as Record<string, unknown>)[column.key] ?? '') }}
              </slot>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 浮动批量操作条：spring 上浮，随选择出现 -->
    <AnimatePresence>
      <Motion
        v-if="selectable && selectedCount > 0"
        tag="div"
        class="fixed bottom-[calc(1.5rem+env(safe-area-inset-bottom))] left-1/2 z-40 flex max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-3 rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-surface-raised)] px-4 py-2.5"
        role="toolbar"
        aria-label="批量操作"
        :initial="{ opacity: 0, y: 24, scale: 0.96 }"
        :animate="{ opacity: 1, y: 0, scale: 1 }"
        :exit="{ opacity: 0, y: 16, scale: 0.97 }"
        :transition="{ type: 'spring', stiffness: 380, damping: 30 }"
      >
        <span class="font-mono-data whitespace-nowrap text-sm font-medium text-[var(--color-text)]">
          已选 {{ selectedCount }} 项
        </span>
        <span class="h-5 w-px bg-[var(--color-border)]" />
        <slot
          name="batch"
          :selected-keys="[...selected]"
          :clear="clearSelection"
        />
        <button
          type="button"
          class="icon-btn-sm"
          aria-label="取消全选"
          @click="clearSelection"
        >
          <UiIcon
            name="close"
            :size="15"
          />
        </button>
      </Motion>
    </AnimatePresence>
  </div>
</template>
