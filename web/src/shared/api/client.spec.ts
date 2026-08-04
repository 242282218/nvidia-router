import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, apiRequest } from './client'

describe('apiRequest', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('uses same-origin credentials and lets the browser supply Origin on writes', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ authenticated: true, must_change_password: true }), {
        headers: { 'Content-Type': 'application/json' },
        status: 200,
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/admin/api/auth/login', {
      body: { password: 'admin', username: 'admin' },
      method: 'POST',
    })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers)
    expect(init.credentials).toBe('same-origin')
    expect(headers.has('Origin')).toBe(false)
    expect(headers.get('Content-Type')).toBe('application/json')
  })

  it('does not add Origin to safe reads', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ authenticated: true, must_change_password: false }), {
        status: 200,
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/admin/api/auth/session')

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(new Headers(init.headers).has('Origin')).toBe(false)
  })

  it('parses the OpenAI error shape into a typed ApiError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: 'invalid_credentials',
              message: 'The username or password is incorrect.',
              param: null,
              type: 'authentication_error',
            },
          }),
          { headers: { 'Retry-After': '60' }, status: 401 },
        ),
      ),
    )

    const request = apiRequest('/admin/api/auth/login', {
      body: { password: 'wrong', username: 'admin' },
      method: 'POST',
    })

    await expect(request).rejects.toMatchObject({
      code: 'invalid_credentials',
      message: 'The username or password is incorrect.',
      name: 'ApiError',
      param: null,
      retryAfter: '60',
      status: 401,
      type: 'authentication_error',
    } satisfies Partial<ApiError>)
  })

  it('dispatches a session-expired event only for invalid_session 401 responses', async () => {
    const onSessionExpired = vi.fn()
    window.addEventListener('session-expired', onSessionExpired)

    // A login failure also returns 401 with code `invalid_credentials`; that
    // must NOT be treated as a stale-session signal.
    const credentialsResponse = new Response(
      JSON.stringify({
        error: { code: 'invalid_credentials', message: 'bad password', param: null, type: 'authentication_error' },
      }),
      { headers: { 'Content-Type': 'application/json' }, status: 401 },
    )
    const sessionResponse = new Response(
      JSON.stringify({
        error: { code: 'invalid_session', message: 'expired', param: null, type: 'authentication_error' },
      }),
      { headers: { 'Content-Type': 'application/json' }, status: 401 },
    )
    const fetchMock = vi.fn().mockResolvedValueOnce(credentialsResponse).mockResolvedValueOnce(sessionResponse)
    vi.stubGlobal('fetch', fetchMock)

    try {
      await expect(apiRequest('/admin/api/auth/login', { method: 'POST' })).rejects.toMatchObject({ status: 401 })
      expect(onSessionExpired).not.toHaveBeenCalled()

      await expect(apiRequest('/admin/api/models')).rejects.toMatchObject({ status: 401 })
      expect(onSessionExpired).toHaveBeenCalledOnce()
    } finally {
      window.removeEventListener('session-expired', onSessionExpired)
    }
  })

  it('never writes submitted secrets to Web Storage', async () => {
    const localSetItem = vi.spyOn(Storage.prototype, 'setItem')
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/admin/api/nvidia-keys', {
      body: { key: 'nvapi-super-secret' },
      method: 'POST',
    })

    expect(localSetItem).not.toHaveBeenCalled()
    expect(JSON.stringify(localStorage)).not.toContain('nvapi-super-secret')
    expect(JSON.stringify(sessionStorage)).not.toContain('nvapi-super-secret')
  })
})
