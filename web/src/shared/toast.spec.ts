import { describe, expect, it } from 'vitest'

import { clearToasts, dismiss, pauseDismiss, resumeDismiss, toastError, toastSuccess, toastState } from './toast'

describe('toast', () => {
  it('caps the visible stack at 3, evicting the oldest', () => {
    clearToasts()
    toastSuccess('a')
    toastSuccess('b')
    toastSuccess('c')
    const fourth = toastSuccess('d')
    expect(toastState.toasts.map((t) => t.message)).toEqual(['b', 'c', 'd'])
    expect(toastState.toasts.some((t) => t.id === fourth)).toBe(true)
  })

  it('de-duplicates an identical toast instead of stacking', () => {
    clearToasts()
    const first = toastError('save failed')
    const second = toastError('save failed')
    expect(second).toBe(first)
    expect(toastState.toasts).toHaveLength(1)
  })

  it('pause/resume keeps the toast alive and restarts the window', () => {
    clearToasts()
    const id = toastSuccess('kept')
    pauseDismiss(id)
    expect(toastState.toasts.some((t) => t.id === id)).toBe(true)
    resumeDismiss(id, 'success')
    expect(toastState.toasts.some((t) => t.id === id)).toBe(true)
  })

  it('dismiss removes the toast immediately', () => {
    clearToasts()
    const id = toastSuccess('bye')
    dismiss(id)
    expect(toastState.toasts).toHaveLength(0)
  })
})
