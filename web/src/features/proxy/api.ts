import { apiRequest } from '../../shared/api/client'
import type { ProxyPoolPatch, ProxyPoolResponse } from './types'

export interface ProxyPoolApi {
  get(signal?: AbortSignal): Promise<ProxyPoolResponse>
  update(patch: ProxyPoolPatch, signal?: AbortSignal): Promise<ProxyPoolResponse>
}

export const proxyPoolApi: ProxyPoolApi = {
  get(signal) {
    return apiRequest('/admin/api/proxy-pool', { signal })
  },
  update(patch, signal) {
    return apiRequest('/admin/api/proxy-pool', { method: 'PATCH', body: patch, signal })
  },
}
