import { defineComponent, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useFocusTrap } from './useFocusTrap'

describe('useFocusTrap', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('cycles focus inside the panel and restores the opener after close', async () => {
    const onClose = vi.fn()
    const open = ref(false)
    const panel = ref<HTMLElement | null>(null)
    const Host = defineComponent({
      setup() {
        useFocusTrap(open, panel, onClose)
        return { open, panel }
      },
      template: `
        <div>
          <div v-if="open" ref="panel" role="dialog">
            <button type="button">第一个</button>
            <button type="button">最后一个</button>
          </div>
        </div>
      `,
    })
    const wrapper = mount(Host, { attachTo: document.body })
    const opener = document.createElement('button')
    opener.type = 'button'
    opener.textContent = '打开'
    document.body.append(opener)
    opener.focus()

    open.value = true
    await nextTick()
    await nextTick()

    const buttons = wrapper.findAll('[role="dialog"] button')
    const first = buttons[0]!.element as HTMLButtonElement
    const last = buttons[1]!.element as HTMLButtonElement
    expect(document.activeElement).toBe(first)

    last.focus()
    const forward = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.dispatchEvent(forward)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(first)

    first.focus()
    const backward = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true })
    document.dispatchEvent(backward)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(last)

    open.value = false
    await nextTick()
    expect(document.activeElement).toBe(opener)
    expect(onClose).not.toHaveBeenCalled()
  })
})
