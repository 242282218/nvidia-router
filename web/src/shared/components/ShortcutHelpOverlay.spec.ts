import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import ShortcutHelpOverlay from './ShortcutHelpOverlay.vue'

describe('ShortcutHelpOverlay', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('focuses the help dialog and restores the opener after close', async () => {
    const opener = document.createElement('button')
    opener.type = 'button'
    opener.textContent = '快捷键帮助'
    document.body.append(opener)
    opener.focus()

    mount(ShortcutHelpOverlay, { attachTo: document.body })
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))
    await flushPromises()

    const close = document.body.querySelector<HTMLButtonElement>('button[aria-label="关闭帮助"]')
    expect(close).not.toBeNull()
    expect(document.activeElement).toBe(close)

    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    document.dispatchEvent(tab)
    expect(tab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(close)

    close?.click()
    await flushPromises()
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })
})
