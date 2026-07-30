import { apiRequest } from '../../shared/api/client'

export interface SessionResponse {
  authenticated: boolean
  must_change_password: boolean
}

export interface AuthApi {
  changePassword(currentPassword: string, newPassword: string): Promise<SessionResponse>
  getSession(): Promise<SessionResponse>
  login(username: string, password: string): Promise<SessionResponse>
  logout(): Promise<void>
}

export const authApi: AuthApi = {
  changePassword(currentPassword, newPassword) {
    return apiRequest('/admin/api/auth/change-password', {
      body: {
        current_password: currentPassword,
        new_password: newPassword,
      },
      method: 'POST',
    })
  },

  getSession() {
    return apiRequest('/admin/api/auth/session')
  },

  login(username, password) {
    return apiRequest('/admin/api/auth/login', {
      body: { password, username },
      method: 'POST',
    })
  },

  logout() {
    return apiRequest('/admin/api/auth/logout', { method: 'POST' })
  },
}
