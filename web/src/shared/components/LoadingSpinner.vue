<script setup lang="ts">
// Single spinner implementation. Before this component the same SVG was
// pasted in eight views alongside a second CSS-border variant — the two
// styles disagreed on size, colour and role attributes.
defineOptions({ name: 'LoadingSpinner' })

withDefaults(defineProps<{
  /** Announced to screen readers and shown next to the icon. */
  label?: string
  size?: 'sm' | 'md'
  /** Icon-only mode for tight spots like button interiors; the label is
   * still announced via aria-label. */
  showLabel?: boolean
}>(), { label: '加载中…', size: 'md', showLabel: true })
</script>

<template>
  <div
    class="flex items-center gap-3 text-sm text-[var(--color-text-muted)]"
    role="status"
    :aria-label="label"
  >
    <svg
      class="animate-spin"
      :class="size === 'sm' ? 'h-3.5 w-3.5' : 'h-4 w-4'"
      fill="none"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <circle
        class="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        stroke-width="4"
      />
      <path
        class="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
    <span v-if="showLabel">{{ label }}</span>
  </div>
</template>
