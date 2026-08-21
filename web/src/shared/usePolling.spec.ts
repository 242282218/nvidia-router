import { defineComponent, h, KeepAlive, ref, type Component } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { usePolling } from './usePolling'

function setHidden(hidden: boolean): void {
  Object.defineProperty(document, 'hidden', { value: hidden, configurable: true })
  document.dispatchEvent(new Event('visibilitychange'))
}

// One shared component definition per distinct shape keeps the file within the
// one-component-per-file lint rule while each test still mounts its own
// instance.
const PollingHost = (task: () => void, intervalMs: number): Component =>
  // eslint-disable-next-line vue/one-component-per-file -- test helper, not a real component file
  defineComponent({
    setup() {
      usePolling(task, intervalMs)
      return () => null
    },
  })

function mountPolling(task: () => void, intervalMs: number): ReturnType<typeof mount> {
  return mount(PollingHost(task, intervalMs))
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

  it('suspends while deactivated and refreshes immediately on activated (KeepAlive)', async () => {
    const task = vi.fn()
    const showPane = ref(true)
    const Pane = PollingHost(task, 5000)
    // eslint-disable-next-line vue/one-component-per-file -- test harness wrapper
    const wrapper = mount(defineComponent({
      setup() {
        return () => h('div', [
          h(KeepAlive, () => (showPane.value ? h(Pane) : null)),
        ])
      },
    }))
    // Mounting inside KeepAlive fires activated once → immediate refresh
    // (same contract as returning from a hidden tab).
    await flushPromises()
    expect(task).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(5000)
    expect(task).toHaveBeenCalledTimes(2)

    // Switch away: the pane deactivates (cached, not unmounted) and polling
    // must stop instead of ticking against invisible endpoints.
    showPane.value = false
    await flushPromises()
    vi.advanceTimersByTime(20_000)
    expect(task).toHaveBeenCalledTimes(2)

    // Switch back: immediate refresh, then the interval resumes.
    showPane.value = true
    await flushPromises()
    expect(task).toHaveBeenCalledTimes(3)
    vi.advanceTimersByTime(5000)
    expect(task).toHaveBeenCalledTimes(4)
    wrapper.unmount()
  })

  it('never fires the activation refresh outside KeepAlive', async () => {
    const task = vi.fn()
    const wrapper = mountPolling(task, 5000)
    await flushPromises()
    // Plain mounts do not trigger onActivated, so only interval ticks run.
    expect(task).toHaveBeenCalledTimes(0)
    vi.advanceTimersByTime(5000)
    expect(task).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
