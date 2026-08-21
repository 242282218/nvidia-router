import { onScopeDispose, ref } from 'vue'

import type { IconName } from '../ui/icons'

// 命令面板扩展注册表：功能模块可向面板注入业务命令
// （如"新建 Access Key"深链到 /access-keys?create=1）。
// 导航命令仍以路由 meta 为唯一事实来源，这里只收增量。

export interface CommandEntry {
  id: string
  label: string
  group: string
  icon: IconName
  keywords?: string
  run: () => void | Promise<void>
}

const extraCommands = ref<CommandEntry[]>([])

/** 注册业务命令；作用域销毁时自动移除。 */
export function registerCommands(entries: readonly CommandEntry[]): void {
  const ids = new Set(entries.map((entry) => entry.id))
  extraCommands.value = [
    ...extraCommands.value.filter((existing) => !ids.has(existing.id)),
    ...entries,
  ]
  onScopeDispose(() => {
    extraCommands.value = extraCommands.value.filter(
      (existing) => !ids.has(existing.id),
    )
  })
}

/** 面板读取的增量命令（只读快照）。 */
export function peekExtraCommands(): CommandEntry[] {
  return [...extraCommands.value]
}
