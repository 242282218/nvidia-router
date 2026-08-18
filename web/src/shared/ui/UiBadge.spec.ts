import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import UiBadge from './UiBadge.vue'

describe('UiBadge', () => {
  it('maps variant to the badge class contract used by specs and e2e', () => {
    for (const variant of ['success', 'warning', 'danger', 'muted', 'info'] as const) {
      const wrapper = mount(UiBadge, { props: { variant, label: '状态' } })
      expect(wrapper.classes()).toContain(`badge-${variant}`)
      expect(wrapper.text()).toContain('状态')
    }
  })

  it('shows the shape marker by default and hides it on request', () => {
    const withDot = mount(UiBadge, { props: { variant: 'success', label: '启用' } })
    expect(withDot.find('span[aria-hidden="true"]').exists()).toBe(true)
    const withoutDot = mount(UiBadge, { props: { variant: 'success', label: '启用', dot: false } })
    expect(withoutDot.find('span[aria-hidden="true"]').exists()).toBe(false)
  })
})
