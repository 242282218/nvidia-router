import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { usePolling } from './usePolling'

function setHidden(hidden: boolean): void {
  Object.defineProperty(document, 'hidden', { value: hidden, configurable: true })
  document.dispatchEvent(new Event('visibilitychange'))
}

function mountPolling(task: () => void, intervalMs: number): ReturnType<typeof mount> {
  return mount(defineComponent({
    setup() {
      usePolling(task, intervalMs)
      return () => null
    },
  }))
}

describe('usePolling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('runs the task on each interval tick', () => {
    const task = vi.fn()
    const wrapper = mountPolling(task, 5000)
    expect(task).not.toHaveBeenCalled()
    vi.advanceTimersByTime(5000)
    expect(task).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(10_000)
    expect(task).toHaveBeenCalledTimes(3)
    wrapper.unmount()
  })

  it('stops ticking while the tab is hidden and resumes immediately on show', () => {
    const task = vi.fn()
    const wrapper = mountPolling(task, 5000)

    setHidden(true)
    vi.advanceTimersByTime(20_000)
    expect(task).not.toHaveBeenCalled()

    setHidden(false)
    expect(task).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(5000)
    expect(task).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('clears the interval on unmount', () => {
    const task = vi.fn()
    const wrapper = mountPolling(task, 5000)
    wrapper.unmount()
    vi.advanceTimersByTime(60_000)
    expect(task).not.toHaveBeenCalled()
  })
})
