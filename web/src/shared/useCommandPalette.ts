import { ref } from 'vue'

// 命令面板全局开关：AppShell 的搜索按钮、快捷键与面板本体共享这一份状态。
const open = ref(false)

export function useCommandPalette() {
  function show(): void {
    open.value = true
  }

  function hide(): void {
    open.value = false
  }

  function toggle(): void {
    open.value = !open.value
  }

  return { open, show, hide, toggle }
}
