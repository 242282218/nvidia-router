import { apiRequest } from '../../shared/api/client'
import type { AccessKeysResponse, CreatedAccessKey } from './types'

export interface AccessKeysApi {
  list(): Promise<AccessKeysResponse>
  create(name: string): Promise<CreatedAccessKey>
  revoke(id: number): Promise<void>
}

export const accessKeysApi: AccessKeysApi = {
  list() {
    return apiRequest('/admin/api/access-keys')
  },
  create(name) {
    return apiRequest('/admin/api/access-keys', { method: 'POST', body: { name } })
  },
  revoke(id) {
    return apiRequest(`/admin/api/access-keys/${id}`, { method: 'DELETE' })
  },
}
