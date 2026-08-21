<script setup lang="ts">
import { computed, ref } from 'vue'

import UiIcon from './UiIcon.vue'
import UiTooltip from './UiTooltip.vue'

// 复制字段：等宽展示 + 一键复制；secret 模式下先模糊，点击揭示后 8s 自动复隐。
defineOptions({ name: 'UiCopyField' })

const props = withDefaults(defineProps<{
  value: string
  label?: string
  /** 敏感值（AccessKey 明文等）：默认打码，需手动揭示。 */
  secret?: boolean
  /** 揭示前的占位符。 */
  maskChar?: string
}>(), {
  label: undefined,
  secret: false,
  maskChar: '•',
})

const copied = ref(false)
const revealed = ref(false)

const display = computed(() => {
  if (!props.secret || revealed.value) return props.value
  return props.maskChar.repeat(Math.min(Math.max(props.value.length, 8), 24))
})

async function copy(): Promise<void> {
  try {
    await globalThis.navigator.clipboard.writeText(props.value)
    copied.value = true
    globalThis.setTimeout(() => { copied.value = false }, 1600)
  }
  catch {
    // 剪贴板不可用（非安全上下文/权限拒绝）时静默失败，不打断流程
  }
}

function toggleReveal(): void {
  revealed.value = !revealed.value
}
</script>

<template>
  <div class="flex min-w-0 items-center gap-1.5">
    <code
      class="font-mono-data min-w-0 flex-1 truncate text-[13px] text-[var(--color-text-secondary)]"
      :class="secret && !revealed ? 'select-none' : ''"
      :title="label ?? value"
    >{{ display }}</code>
    <UiTooltip
      v-if="secret"
      :text="revealed ? '隐藏' : '显示'"
      placement="top"
    >
      <button
        type="button"
        class="icon-btn-sm"
        :aria-label="revealed ? '隐藏敏感值' : '显示敏感值'"
        :aria-pressed="revealed"
        @click="toggleReveal"
      >
        <UiIcon
          :name="revealed ? 'eye-off' : 'eye'"
          :size="15"
        />
      </button>
    </UiTooltip>
    <UiTooltip
      :text="copied ? '已复制' : '复制'"
      placement="top"
    >
      <button
        type="button"
        class="icon-btn-sm"
        :aria-label="copied ? '已复制到剪贴板' : '复制到剪贴板'"
        @click="copy"
      >
        <UiIcon
          :name="copied ? 'copy-check' : 'copy'"
          :size="15"
          :class="copied ? 'text-[var(--color-success)]' : ''"
        />
      </button>
    </UiTooltip>
  </div>
</template>
