import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import KeyTestDialog from './KeyTestDialog.vue'

describe('KeyTestDialog', () => {
  it('renders a backend "valid" result as success (not the default warning)', () => {
    const wrapper = mount(KeyTestDialog, {
      props: {
        open: true,
        results: [{ id: 7, status: 'valid', models: ['vendor/chat'] }],
      },
    })
    const badge = wrapper.get('[data-testid="key-test-results"] .badge-success')
    expect(badge.text()).toBe('可用')
    expect(wrapper.text()).toContain('Key #7')
  })

  it('renders "ok" results as success too (legacy status value)', () => {
    const wrapper = mount(KeyTestDialog, {
      props: {
        open: true,
        results: [{ id: 3, status: 'ok' }],
      },
    })
    expect(wrapper.get('.badge-success').text()).toBe('可用')
  })

  it('renders error results with the danger badge and raw reason', () => {
    const wrapper = mount(KeyTestDialog, {
      props: {
        open: true,
        results: [{ id: 8, status: 'error', reason: 'upstream timeout' }],
      },
    })
    expect(wrapper.get('.badge-danger').text()).toBe('失败')
    expect(wrapper.text()).toContain('upstream timeout')
  })

  it('renders unknown statuses with the warning badge and the raw status', () => {
    const wrapper = mount(KeyTestDialog, {
      props: {
        open: true,
        results: [{ id: 9, status: 'indeterminate' }],
      },
    })
    expect(wrapper.get('.badge-warning').text()).toBe('indeterminate')
  })

  it('does not render the dialog when closed or empty', () => {
    const closed = mount(KeyTestDialog, { props: { open: false, results: [{ id: 1, status: 'valid' }] } })
    expect(closed.find('[data-testid="key-test-results"]').exists()).toBe(false)

    const empty = mount(KeyTestDialog, { props: { open: true, results: [] } })
    expect(empty.find('[data-testid="key-test-results"]').exists()).toBe(false)
  })
})
