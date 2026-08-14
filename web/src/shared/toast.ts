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

// maxVisibleToasts caps the stack so a burst of notifications cannot cover the
// whole viewport (design-aesthetics toast 反模式#6: unbounded stacking). The
// oldest toast is dismissed to make room.
const maxVisibleToasts = 3

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
  // De-duplicate an identical toast (design-aesthetics toast 反模式#7): a rapid
  // re-trigger updates the existing toast's timer instead of stacking duplicates.
  const duplicate = state.toasts.find((toast) => toast.type === type && toast.message === message)
  if (duplicate !== undefined) {
    scheduleDismiss(duplicate.id, type)
    return duplicate.id
  }

  const id = nextId++
  state.toasts.push({ id, type, message })
  // Enforce the stack cap before scheduling so the new toast is never
  // immediately evicted by its own dismiss timer.
  while (state.toasts.length > maxVisibleToasts) {
    const oldest = state.toasts[0]
    if (oldest === undefined) break
    dismiss(oldest.id)
  }
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

// pauseDismiss cancels the auto-dismiss timer without removing the toast, so a
// hovered or focused toast stays readable (design-aesthetics toast: pause on
// hover/focus). resumeDismiss restarts the full window from scratch — the user
// may have finished reading and is confirming the message again.
export function pauseDismiss(id: number): void {
  const timer = timers.get(id)
  if (timer === undefined) return
  clearTimeout(timer)
  timers.delete(id)
}

export function resumeDismiss(id: number, type: ToastType): void {
  scheduleDismiss(id, type)
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
