import { onScopeDispose, readonly, ref, type Ref } from 'vue'

// 全局快捷键注册表：模块级单例，AppShell 挂载一次监听器。
// 组合格式：'mod+k'（mod = macOS ⌘ / 其他 Ctrl）、'shift+?'、'/'、'escape'。
// 纯字母/符号快捷键在输入焦点（input/textarea/select/contenteditable）内不触发，
// 带 mod 的组合与 escape 始终触发。

export interface HotkeyEntry {
  id: string
  combo: string
  /** 帮助浮层里的中文说明。 */
  description: string
  /** 帮助浮层的分组标题。 */
  group: string
  handler: (event: KeyboardEvent) => void
}

interface ParsedCombo {
  mod: boolean
  shift: boolean
  key: string
}

const registry = ref<HotkeyEntry[]>([])
let listenerInstalled = false

function parseCombo(combo: string): ParsedCombo {
  const parts = combo.toLowerCase().split('+')
  const key = parts[parts.length - 1] ?? ''
  return {
    mod: parts.includes('mod'),
    shift: parts.includes('shift'),
    key,
  }
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof globalThis.HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

function matches(event: KeyboardEvent, parsed: ParsedCombo): boolean {
  const modPressed = event.metaKey || event.ctrlKey
  if (parsed.mod !== modPressed) return false
  // '?' 这类符号本身按 shift+key 产生，不强制比对 shiftKey
  if (parsed.shift && !event.shiftKey && event.key !== parsed.key) return false
  return event.key.toLowerCase() === parsed.key
}

function onKeydown(event: KeyboardEvent): void {
  for (const entry of registry.value) {
    const parsed = parseCombo(entry.combo)
    if (!parsed.mod && isEditableTarget(event.target)) continue
    if (!matches(event, parsed)) continue
    event.preventDefault()
    entry.handler(event)
    return
  }
}

function ensureListener(): void {
  if (listenerInstalled) return
  globalThis.addEventListener('keydown', onKeydown)
  listenerInstalled = true
}

/** 注册全局快捷键；作用域销毁时自动注销。返回注册表只读视图的引用。 */
export function registerHotkey(entry: HotkeyEntry): void {
  ensureListener()
  registry.value = [...registry.value.filter((e) => e.id !== entry.id), entry]
  onScopeDispose(() => {
    registry.value = registry.value.filter((e) => e.id !== entry.id)
  })
}

/** 帮助浮层读取的只读注册表。 */
export function useHotkeyRegistry(): Readonly<Ref<readonly HotkeyEntry[]>> {
  return readonly(registry)
}

/** 展示用：把 combo 格式化为按键序列（⌘K / Shift+/ 等）。 */
export function formatCombo(combo: string): string[] {
  const parts = combo.split('+')
  return parts.map((part) => {
    const lower = part.toLowerCase()
    if (lower === 'mod') return 'Ctrl'
    if (lower === 'shift') return 'Shift'
    if (part === '?') return 'Shift+/'
    if (lower === 'escape') return 'Esc'
    return part.length === 1 ? part.toUpperCase() : part
  })
}
