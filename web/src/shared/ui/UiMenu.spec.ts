import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import UiMenu from './UiMenu.vue'

// UiMenu 交互契约：触发钮开合、Esc 关闭并归还焦点、slot close() 收起。
const Template = {
  components: { UiMenu },
  template: `
    <div>
      <UiMenu label="测试菜单">
        <template #default="{ close }">
          <button class="menu-item" role="menuitem" type="button" data-testid="item" @click="close()">动作</button>
        </template>
      </UiMenu>
    </div>
  `,
}

function mountMenu(): ReturnType<typeof mount> {
  return mount(Template, { attachTo: document.body })
}

describe('UiMenu', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('keeps the panel closed until the trigger is clicked', async () => {
    const wrapper = mountMenu()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)

    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')
    // 不用 isVisible：motion-v 在测试环境停留在初始 opacity:0，可见性断言会误报
    expect(wrapper.find('[role="menu"]').exists()).toBe(true)
    expect(wrapper.get('button[aria-haspopup="menu"]').attributes('aria-expanded')).toBe('true')
  })

  it('runs the item action path by exposing close() through the slot', async () => {
    const wrapper = mountMenu()
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click')
    await wrapper.get('[data-testid="item"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
  })

  it('closes on Escape and returns focus to the trigger button', async () => {
    const wrapper = mountMenu()
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    await trigger.trigger('click')
    expect(wrapper.find('[role="menu"]').exists()).toBe(true)

    await trigger.trigger('keydown.escape')
    await flushPromises()

    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)
  })
})
