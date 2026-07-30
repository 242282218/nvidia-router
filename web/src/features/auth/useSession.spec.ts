import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/client'
import type { AuthApi } from './api'
import { createSessionStore } from './useSession'

function createAuthApi(): AuthApi {
  return {
    changePassword: vi.fn(),
    getSession: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
  }
}

describe('createSessionStore', () => {
  it('moves through unknown, anonymous, forced-change and authenticated states', async () => {
    const api = createAuthApi()
    const store = createSessionStore(api)
    expect(store.state.value).toEqual({ kind: 'unknown' })

    vi.mocked(api.getSession).mockRejectedValueOnce(
      new ApiError(401, {
        code: 'invalid_session',
        message: 'The administrator session is invalid or expired.',
        param: null,
        type: 'authentication_error',
      }),
    )
    await store.refresh()
    expect(store.state.value).toEqual({ kind: 'anonymous' })

    vi.mocked(api.login).mockResolvedValueOnce({
      authenticated: true,
      must_change_password: true,
    })
    await store.login('admin', 'admin')
    expect(store.state.value).toEqual({ kind: 'authenticated', mustChangePassword: true })

    vi.mocked(api.changePassword).mockResolvedValueOnce({
      authenticated: true,
      must_change_password: false,
    })
    await store.changePassword('admin', 'replacement-password')
    expect(store.state.value).toEqual({ kind: 'authenticated', mustChangePassword: false })
  })

  it('does not persist login passwords or session data in Web Storage', async () => {
    const api = createAuthApi()
    vi.mocked(api.login).mockResolvedValue({
      authenticated: true,
      must_change_password: false,
    })
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const store = createSessionStore(api)

    await store.login('admin', 'submitted-password')

    expect(storageWrite).not.toHaveBeenCalled()
  })
})
