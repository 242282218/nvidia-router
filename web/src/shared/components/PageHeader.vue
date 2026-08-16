<script setup lang="ts">
// Page header: eyebrow group label + h1 + subtitle + action slot. The
// eyebrow colour was per-page (accent/warning/info) before the refactor,
// which scattered the accent across every screen; it is now the neutral
// subtle token on all pages.
defineOptions({ name: 'PageHeader' })

defineProps<{
  eyebrow: string
  title: string
  subtitle?: string
}>()
</script>

<template>
  <header class="section-header">
    <div>
      <p class="text-xs font-medium uppercase tracking-[0.12em] text-[var(--color-text-subtle)]">
        {{ eyebrow }}
      </p>
      <h1 class="page-title mt-1">
        {{ title }}
      </h1>
      <p
        v-if="subtitle"
        class="page-subtitle"
      >
        {{ subtitle }}
      </p>
    </div>
    <div
      v-if="$slots.actions"
      class="flex flex-wrap items-center gap-2"
    >
      <slot name="actions" />
    </div>
  </header>
</template>
