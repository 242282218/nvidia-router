import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import UiTooltip from './UiTooltip.vue'

describe('UiTooltip', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('aligns the tooltip inward when its trigger is near the viewport edge', async () => {
    vi.useFakeTimers()
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      width: 36,
      height: 36,
      top: 80,
      right: 936,
      bottom: 116,
      left: 900,
      x: 900,
      y: 80,
      toJSON: () => ({}),
    } as DOMRect)

    const wrapper = mount(UiTooltip, {
      props: { text: '这是一个较长的提示文本' },
      slots: { default: '<button type="button">触发</button>' },
    })

    await wrapper.get('span.relative').trigger('mouseenter')
    vi.advanceTimersByTime(350)
    await flushPromises()

    const tooltip = wrapper.get('[role="tooltip"]')
    expect(tooltip.element.parentElement?.classList).toContain('right-0')
    expect(tooltip.element.parentElement?.classList).not.toContain('inset-x-0')
  })
})
