import { inject, ref, type InjectionKey, type Ref } from 'vue'

import { ApiError, SESSION_EXPIRED_EVENT } from '../../shared/api/client'
import { authApi, type AuthApi, type SessionResponse } from './api'

export type SessionState =
  | { kind: 'unknown' }
  | { kind: 'anonymous' }
  | { kind: 'authenticated'; mustChangePassword: boolean }

export interface SessionStore {
  state: Ref<SessionState>
  changePassword(currentPassword: string, newPassword: string): Promise<void>
  dispose(): void
  ensureLoaded(): Promise<void>
  login(username: string, password: string): Promise<void>
  logout(): Promise<void>
  refresh(): Promise<void>
}

export const sessionKey: InjectionKey<SessionStore> = Symbol('admin-session')

export function createSessionStore(api: AuthApi = authApi): SessionStore {
  const state = ref<SessionState>({ kind: 'unknown' })
  let loading: Promise<void> | undefined

  function expireSession(): void {
    state.value = { kind: 'anonymous' }
  }

  window.addEventListener(SESSION_EXPIRED_EVENT, expireSession)

  function dispose(): void {
    window.removeEventListener(SESSION_EXPIRED_EVENT, expireSession)
  }

  async function refresh(): Promise<void> {
    try {
      state.value = toSessionState(await api.getSession())
    } catch (error) {
      if (state.value.kind === 'unknown' || (error instanceof ApiError && error.status === 401)) {
        expireSession()
        return
      }
      throw error
    }
  }

  async function ensureLoaded(): Promise<void> {
    if (state.value.kind !== 'unknown') {
      return
    }
    loading ??= refresh().finally(() => {
      loading = undefined
    })
    await loading
  }

  async function login(username: string, password: string): Promise<void> {
    state.value = toSessionState(await api.login(username, password))
  }

  async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
    state.value = toSessionState(await api.changePassword(currentPassword, newPassword))
  }

  async function logout(): Promise<void> {
    await api.logout()
    state.value = { kind: 'anonymous' }
  }

  return { changePassword, dispose, ensureLoaded, login, logout, refresh, state }
}

export function useSession(): SessionStore {
  const session = inject(sessionKey)
  if (!session) {
    throw new Error('SessionStore is not provided.')
  }
  return session
}

function toSessionState(response: SessionResponse): SessionState {
  if (!response.authenticated) {
    return { kind: 'anonymous' }
  }
  return {
    kind: 'authenticated',
    mustChangePassword: response.must_change_password,
  }
}
