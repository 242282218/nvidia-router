import { reactive, readonly } from 'vue'

export type ToastType = 'success' | 'error' | 'info' | 'warning'

export interface Toast {
  id: number
  type: ToastType
  message: string
}

interface ToastState {
  toasts: Toast[]
}

const state = reactive<ToastState>({ toasts: [] })

let nextId = 1

const defaultDuration: Record<ToastType, number> = {
  success: 3200,
  info: 3200,
  warning: 5200,
  // Errors carry actionable detail; keep them readable instead of auto-dismissing
  // while the user is still looking at the page that produced them.
  error: 7000,
}

const timers = new Map<number, ReturnType<typeof setTimeout>>()

function scheduleDismiss(id: number, type: ToastType): void {
  const existing = timers.get(id)
  if (existing !== undefined) clearTimeout(existing)
  timers.set(
    id,
    setTimeout(() => dismiss(id), defaultDuration[type]),
  )
}

function push(type: ToastType, message: string): number {
  const id = nextId++
  state.toasts.push({ id, type, message })
  scheduleDismiss(id, type)
  return id
}

export function dismiss(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) {
    clearTimeout(timer)
    timers.delete(id)
  }
  const index = state.toasts.findIndex((toast) => toast.id === id)
  if (index !== -1) state.toasts.splice(index, 1)
}

export function toastSuccess(message: string): number {
  return push('success', message)
}

export function toastError(message: string): number {
  return push('error', message)
}

export function toastInfo(message: string): number {
  return push('info', message)
}

export function toastWarning(message: string): number {
  return push('warning', message)
}

/** Dismisses every live toast. Used by tests between cases; harmless at runtime. */
export function clearToasts(): void {
  for (const id of [...timers.keys()]) dismiss(id)
}

export const toastState = readonly(state)
