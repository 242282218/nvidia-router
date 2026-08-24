import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import UiModal from './UiModal.vue'

describe('UiModal', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('mounts the open surface directly in the document overlay layer', async () => {
    const wrapper = mount(UiModal, {
      props: { open: true, title: '测试对话框' },
      global: { stubs: { Transition: false } },
    })

    await flushPromises()

    const overlay = document.body.querySelector<HTMLElement>('.modal-overlay')
    expect(overlay).not.toBeNull()
    expect(overlay?.parentElement).toBe(document.body)
    expect(document.body.style.overflow).toBe('hidden')
    expect(document.activeElement).toBe(overlay?.querySelector('[role="dialog"] button'))

    wrapper.unmount()
  })
})
