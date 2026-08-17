import { apiRequest } from '../../shared/api/client'
import type { AccessKeyPolicy, AccessKeysResponse, CreatedAccessKey } from './types'

export interface AccessKeysApi {
  list(): Promise<AccessKeysResponse>
  create(name: string): Promise<CreatedAccessKey>
  updatePolicy(id: number, policy: AccessKeyPolicy): Promise<void>
  revoke(id: number): Promise<void>
  delete(id: number): Promise<void>
}

export const accessKeysApi: AccessKeysApi = {
  list() {
    return apiRequest('/admin/api/access-keys')
  },
  create(name) {
    return apiRequest('/admin/api/access-keys', { method: 'POST', body: { name } })
  },
  updatePolicy(id, policy) {
    return apiRequest(`/admin/api/access-keys/${id}`, { method: 'PATCH', body: policy })
  },
  revoke(id) {
    return apiRequest(`/admin/api/access-keys/${id}/revoke`, { method: 'POST' })
  },
  delete(id) {
    return apiRequest(`/admin/api/access-keys/${id}`, { method: 'DELETE' })
  },
}
