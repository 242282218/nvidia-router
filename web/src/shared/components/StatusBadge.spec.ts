import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import StatusBadge from './StatusBadge.vue'

describe('StatusBadge', () => {
  it('maps variant to the badge class contract used by specs and e2e', () => {
    for (const variant of ['success', 'warning', 'danger', 'muted', 'info'] as const) {
      const wrapper = mount(StatusBadge, { props: { variant, label: '状态' } })
      expect(wrapper.classes()).toContain(`badge-${variant}`)
      expect(wrapper.text()).toBe('状态')
    }
  })

  it('shows the shape marker by default and hides it on request', () => {
    const withDot = mount(StatusBadge, { props: { variant: 'success', label: '启用' } })
    expect(withDot.find('span[aria-hidden="true"]').exists()).toBe(true)
    const withoutDot = mount(StatusBadge, { props: { variant: 'success', label: '启用', dot: false } })
    expect(withoutDot.find('span[aria-hidden="true"]').exists()).toBe(false)
  })
})
