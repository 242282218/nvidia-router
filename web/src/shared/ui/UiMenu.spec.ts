import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import UiMenu from './UiMenu.vue'

// UiMenu 交互契约：触发钮开合、Esc 关闭并归还焦点、slot close() 收起。
const Template = {
  components: { UiMenu },
  template: `
    <div>
      <UiMenu label="测试菜单">
        <template #default="{ close }">
          <button class="menu-item" role="menuitem" type="button" data-testid="item" @click="close()">动作</button>
          <button class="menu-item" role="menuitem" type="button" data-testid="secondary-item">第二动作</button>
        </template>
      </UiMenu>
    </div>
  `,
}

function mountMenu(): ReturnType<typeof mount> {
  const wrapper = mount(Template, { attachTo: document.body })
  mountedMenus.push(wrapper)
  return wrapper
}

const mountedMenus: Array<{ unmount: () => void }> = []

describe('UiMenu', () => {
  afterEach(() => {
    for (const wrapper of mountedMenus.splice(0)) wrapper.unmount()
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('keeps the panel closed until the trigger is clicked', async () => {
    const wrapper = mountMenu()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)

    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')
    expect(document.body.querySelector('[role="menu"]')).not.toBeNull()
    expect(wrapper.get('button[aria-haspopup="menu"]').attributes('aria-expanded')).toBe('true')
  })

  it('bounds the menu height and keeps long content scrollable', async () => {
    const wrapper = mountMenu()
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')

    const menu = document.body.querySelector<HTMLElement>('[role="menu"]')
    expect(menu).not.toBeNull()
    expect([...menu!.classList].some((name) => name.startsWith('max-h-'))).toBe(true)
    expect(menu!.classList).toContain('overflow-y-auto')
    expect(menu!.classList).toContain('overscroll-contain')
  })

  it('aligns the panel inward when the trigger is near the viewport edge', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      width: 100,
      height: 36,
      top: 0,
      right: 1000,
      bottom: 36,
      left: 900,
      x: 900,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    const wrapper = mountMenu()
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')

    const menu = document.body.querySelector<HTMLElement>('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(menu?.classList).toContain('fixed')
    expect(menu?.getAttribute('style')).toContain('left:')
  })

  it('runs the item action path by exposing close() through the slot', async () => {
    const wrapper = mountMenu()
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')
    const item = document.body.querySelector<HTMLButtonElement>('[data-testid="item"]')
    expect(item).not.toBeNull()
    item?.click()
    await flushPromises()

    expect(document.body.querySelector('[role="menu"]')).toBeNull()
  })

  it('moves focus into the menu and supports keyboard navigation', async () => {
    const wrapper = mountMenu()
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    ;(trigger.element as HTMLButtonElement).focus()
    await trigger.trigger('click')

    const items = Array.from(document.body.querySelectorAll<HTMLElement>('[role="menu"] [role="menuitem"]'))
    expect(items).toHaveLength(2)
    expect(document.activeElement).toBe(items[0])

    items[0]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(items[1])
    items[1]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(items[0])
    items[0]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(items[1])
    items[1]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(items[0])
  })

  it('closes on Escape and returns focus to the trigger button', async () => {
    const wrapper = mountMenu()
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    await trigger.trigger('click')
    expect(document.body.querySelector('[role="menu"]')).not.toBeNull()

    await trigger.trigger('keydown.escape')
    await flushPromises()

    expect(document.body.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
  })
})
