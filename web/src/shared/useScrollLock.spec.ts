import { describe, expect, it } from 'vitest'

import { lockBodyScroll, unlockBodyScroll } from './useScrollLock'

describe('useScrollLock', () => {
  it('locks and unlocks body scroll with reference counting', () => {
    document.body.style.overflow = ''
    expect(document.body.style.overflow).toBe('')

    lockBodyScroll()
    expect(document.body.style.overflow).toBe('hidden')

    // Second lock maintains hidden
    lockBodyScroll()
    expect(document.body.style.overflow).toBe('hidden')

    // First unlock maintains hidden because lockCount > 0
    unlockBodyScroll()
    expect(document.body.style.overflow).toBe('hidden')

    // Second unlock restores original overflow
    unlockBodyScroll()
    expect(document.body.style.overflow).toBe('')
  })
})
