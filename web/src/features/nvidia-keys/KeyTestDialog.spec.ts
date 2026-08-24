import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import KeyTestDialog from './KeyTestDialog.vue'
import type { KeyTestResult } from './types'

const mountedDialogs: Array<{ unmount: () => void }> = []

afterEach(() => {
  for (const wrapper of mountedDialogs.splice(0)) wrapper.unmount()
  document.body.innerHTML = ''
})

function mountDialog(options: { props: { open: boolean; results: KeyTestResult[] } }) {
  const wrapper = mount(KeyTestDialog, options)
  mountedDialogs.push(wrapper)
  return wrapper
}

function bodyElement<T extends Element>(selector: string): T {
  const element = document.body.querySelector<T>(selector)
  if (!element) throw new Error(`Expected body element: ${selector}`)
  return element
}

describe('KeyTestDialog', () => {
  it('renders a backend "valid" result as success (not the default warning)', () => {
    mountDialog({
      props: {
        open: true,
        results: [{ id: 7, status: 'valid', models: ['vendor/chat'] }],
      },
    })
    const badge = bodyElement<HTMLElement>('[data-testid="key-test-results"] .badge-success')
    expect(badge.textContent?.trim()).toBe('可用')
    expect(bodyElement<HTMLElement>('[data-testid="key-test-results"]').textContent).toContain('Key #7')
  })

  it('renders "ok" results as success too (legacy status value)', () => {
    mountDialog({
      props: {
        open: true,
        results: [{ id: 3, status: 'ok' }],
      },
    })
    expect(bodyElement<HTMLElement>('.badge-success').textContent?.trim()).toBe('可用')
  })

  it('renders error results with the danger badge and raw reason', () => {
    mountDialog({
      props: {
        open: true,
        results: [{ id: 8, status: 'error', reason: 'upstream timeout' }],
      },
    })
    expect(bodyElement<HTMLElement>('.badge-danger').textContent?.trim()).toBe('失败')
    expect(bodyElement<HTMLElement>('[data-testid="key-test-results"]').textContent).toContain('upstream timeout')
  })

  it('renders unknown statuses with the warning badge and the raw status', () => {
    mountDialog({
      props: {
        open: true,
        results: [{ id: 9, status: 'indeterminate' }],
      },
    })
    expect(bodyElement<HTMLElement>('.badge-warning').textContent?.trim()).toBe('indeterminate')
  })

  it('does not render the dialog when closed or empty', () => {
    mountDialog({ props: { open: false, results: [{ id: 1, status: 'valid' }] } })
    expect(document.body.querySelector('[data-testid="key-test-results"]')).toBeNull()

    mountDialog({ props: { open: true, results: [] } })
    expect(document.body.querySelector('[data-testid="key-test-results"]')).toBeNull()
  })
})
