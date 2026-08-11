import { apiRequest } from '../../shared/api/client'
import type { ProviderCredential } from './types'

export interface ProvidersApi {
  list(): Promise<{ data: ProviderCredential[] }>
  create(name: string, baseUrl: string, key: string): Promise<{ data: ProviderCredential }>
  setEnabled(id: number, enabled: boolean): Promise<{ id: number; enabled: boolean }>
}

export const providersApi: ProvidersApi = {
  list() {
    return apiRequest('/admin/api/providers')
  },
  create(name, baseUrl, key) {
    return apiRequest('/admin/api/providers', { method: 'POST', body: { name, base_url: baseUrl, key } })
  },
  setEnabled(id, enabled) {
    return apiRequest(`/admin/api/providers/${id}`, { method: 'PATCH', body: { enabled } })
  },
}
