import { afterEach, describe, expect, it, vi } from 'vitest'

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

type DisposableStore = ReturnType<typeof createSessionStore>

const stores: DisposableStore[] = []

function createStore(api: AuthApi): ReturnType<typeof createSessionStore> {
  const store = createSessionStore(api) as DisposableStore
  stores.push(store)
  return store
}

afterEach(() => {
  for (const store of stores.splice(0)) store.dispose()
})

describe('createSessionStore', () => {
  it('moves through unknown, anonymous, forced-change and authenticated states', async () => {
    const api = createAuthApi()
    const store = createStore(api)
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
    const store = createStore(api)

    await store.login('admin', 'submitted-password')

    expect(storageWrite).not.toHaveBeenCalled()
  })

  it.each([
    ['a 500 response', new ApiError(500, {
      code: 'internal_error',
      message: 'Session probe failed.',
      param: null,
      type: 'server_error',
    })],
    ['a network failure', new TypeError('Network request failed')],
  ])('recovers the first session probe from %s as anonymous', async (_name, failure) => {
    const api = createAuthApi()
    vi.mocked(api.getSession).mockRejectedValueOnce(failure)
    const store = createStore(api)

    await expect(store.ensureLoaded()).resolves.toBeUndefined()
    expect(store.state.value).toEqual({ kind: 'anonymous' })
  })

  it('becomes anonymous on session-expired and stops listening after dispose', async () => {
    const api = createAuthApi()
    vi.mocked(api.login).mockResolvedValue({
      authenticated: true,
      must_change_password: false,
    })
    const store = createStore(api)

    await store.login('admin', 'submitted-password')
    window.dispatchEvent(new Event('session-expired'))
    expect(store.state.value).toEqual({ kind: 'anonymous' })

    await store.login('admin', 'submitted-password')
    expect(store.dispose).toBeTypeOf('function')
    store.dispose()
    window.dispatchEvent(new Event('session-expired'))
    expect(store.state.value).toEqual({ kind: 'authenticated', mustChangePassword: false })
  })

  it('clears local session state even when the remote logout call fails', async () => {
    const api = createAuthApi()
    vi.mocked(api.login).mockResolvedValueOnce({
      authenticated: true,
      must_change_password: false,
    })
    // Network failure or 5xx during logout must not leave the client stuck
    // in the authenticated shell holding a token the server has lost.
    vi.mocked(api.logout).mockRejectedValueOnce(new TypeError('Network request failed'))
    const store = createStore(api)

    await store.login('admin', 'submitted-password')
    await store.logout()

    expect(store.state.value).toEqual({ kind: 'anonymous' })
  })
})
