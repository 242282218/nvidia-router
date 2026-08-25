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
  /** 复制按钮的 data-testid（用于 e2e/单测兼容）。 */
  copyTestId?: string
}>(), {
  label: undefined,
  secret: false,
  maskChar: '•',
  copyTestId: undefined,
})

const copied = ref(false)
const revealed = ref(false)
// 复制结果反馈：clipboard API 不可用（HTTP 明文环境）时走 legacy 降级，
// 成功/失败都要给可见文字（无障碍红线：不能只靠图标变色）。
const copyMessage = ref('')

const display = computed(() => {
  if (!props.secret || revealed.value) return props.value
  return props.maskChar.repeat(Math.min(Math.max(props.value.length, 8), 24))
})

async function copy(): Promise<void> {
  try {
    if (globalThis.navigator.clipboard) {
      await globalThis.navigator.clipboard.writeText(props.value)
    } else {
      legacyCopy(props.value)
    }
    copied.value = true
    copyMessage.value = '已复制。'
    globalThis.setTimeout(() => { copied.value = false }, 1600)
  }
  catch {
    copyMessage.value = '复制失败，请手动复制。'
  }
}

function legacyCopy(value: string): void {
  const input = globalThis.document.createElement('textarea')
  input.value = value
  input.style.position = 'fixed'
  input.style.opacity = '0'
  globalThis.document.body.append(input)
  try {
    input.select()
    const copiedOk = globalThis.document.execCommand('copy')
    if (!copiedOk) throw new Error('legacy copy failed')
  } finally {
    input.value = ''
    input.remove()
  }
}

function toggleReveal(): void {
  revealed.value = !revealed.value
}
</script>

<template>
  <div class="flex min-w-0 flex-wrap items-center gap-1.5">
    <code
      class="font-mono-data min-w-0 flex-1 truncate text-sm text-[var(--color-text-secondary)]"
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
        :data-testid="copyTestId"
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
    <p
      v-if="copyMessage"
      class="w-full text-center text-xs text-[var(--color-text-secondary)]"
      role="status"
    >
      {{ copyMessage }}
    </p>
  </div>
</template>
