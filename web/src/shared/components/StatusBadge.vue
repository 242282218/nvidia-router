<script setup lang="ts">
// Status pill with a shape marker. The dot is the same shape for every
// variant on purpose: the label text is the second encoding (colour alone
// must never carry state), and distinct glyphs per variant would turn five
// tiny icons into the page's loudest element.
defineOptions({ name: 'StatusBadge' })

withDefaults(defineProps<{
  variant: 'success' | 'warning' | 'danger' | 'muted' | 'info'
  label: string
  dot?: boolean
}>(), { dot: true })
</script>

<template>
  <span
    class="gap-1.5"
    :class="`badge-${variant}`"
  >
    <span
      v-if="dot"
      class="h-1.5 w-1.5 shrink-0 rounded-full bg-current"
      aria-hidden="true"
    />
    {{ label }}
  </span>
</template>
