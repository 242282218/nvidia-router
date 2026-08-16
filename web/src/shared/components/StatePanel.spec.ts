import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import StatePanel from './StatePanel.vue'

describe('StatePanel', () => {
  it('accepts kebab-case test-id attrs the way templates pass them', () => {
    const Host = defineComponent({
      components: { StatePanel },
      template: '<StatePanel error="失败。" errorTestId="host-error" retryTestId="host-retry" />',
    })
    const wrapper = mount(Host)
    expect(wrapper.find('[data-testid="host-error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="host-retry"]').exists()).toBe(true)
  })

  it('renders the loading state with the given label and role=status', () => {
    const wrapper = mount(StatePanel, {
      props: { loading: true, loadingLabel: '加载 NVIDIA Key…' },
    })
    expect(wrapper.find('[role="status"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('加载 NVIDIA Key…')
  })

  it('renders a recoverable error state with role=alert and a retry button', async () => {
    const wrapper = mount(StatePanel, {
      props: { error: '加载失败。', errorTestId: 'page-load-error', retryTestId: 'page-retry' },
    })
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('加载失败。')
    const retry = wrapper.find('[data-testid="page-retry"]')
    expect(retry.exists()).toBe(true)
    await retry.trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('renders the empty state with label and optional hint', () => {
    const wrapper = mount(StatePanel, {
      props: { empty: true, emptyLabel: '尚未创建 Access Key。', emptyHint: '点击右上角「创建 Access Key」生成第一条。' },
    })
    expect(wrapper.text()).toContain('尚未创建 Access Key。')
    expect(wrapper.text()).toContain('点击右上角「创建 Access Key」生成第一条。')
  })

  it('falls through to the default slot when no state applies', () => {
    const wrapper = mount(StatePanel, {
      slots: { default: '<p data-testid="content">rows</p>' },
    })
    expect(wrapper.find('[data-testid="content"]').exists()).toBe(true)
  })
})
