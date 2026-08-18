import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import UiPageHeader from './UiPageHeader.vue'

describe('UiPageHeader', () => {
  it('renders eyebrow, single h1 and subtitle', () => {
    const wrapper = mount(UiPageHeader, {
      props: { eyebrow: '安全管理', title: '审计日志', subtitle: '记录所有管理操作。' },
    })
    expect(wrapper.find('h1').text()).toBe('审计日志')
    expect(wrapper.findAll('h1')).toHaveLength(1)
    expect(wrapper.text()).toContain('安全管理')
    expect(wrapper.text()).toContain('记录所有管理操作。')
  })

  it('omits the subtitle node and action container when unused', () => {
    const wrapper = mount(UiPageHeader, {
      props: { eyebrow: '运维管理', title: 'NVIDIA Key' },
    })
    expect(wrapper.find('p.page-subtitle').exists()).toBe(false)
    expect(wrapper.find('header > div + div').exists()).toBe(false)
  })

  it('renders the actions slot next to the title block', () => {
    const wrapper = mount(UiPageHeader, {
      props: { eyebrow: '运维管理', title: 'NVIDIA Key' },
      slots: { actions: '<button data-testid="action">批量导入</button>' },
    })
    expect(wrapper.find('[data-testid="action"]').exists()).toBe(true)
  })
})
