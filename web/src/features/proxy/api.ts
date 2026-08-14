import { apiRequest } from '../../shared/api/client'
import type { PoolStatusResponse, ProxyPoolPatch, ProxyPoolResponse } from './types'

export interface ProxyPoolApi {
  get(signal?: AbortSignal): Promise<ProxyPoolResponse>
  update(patch: ProxyPoolPatch, signal?: AbortSignal): Promise<ProxyPoolResponse>
  status(signal?: AbortSignal): Promise<PoolStatusResponse>
  refresh(signal?: AbortSignal): Promise<{ data: { message: string } }>
}

export const proxyPoolApi: ProxyPoolApi = {
  get(signal) {
    return apiRequest('/admin/api/proxy-pool', { signal })
  },
  update(patch, signal) {
    return apiRequest('/admin/api/proxy-pool', { method: 'PATCH', body: patch, signal })
  },
  status(signal) {
    return apiRequest('/admin/api/proxy-pool/status', { signal })
  },
  refresh(signal) {
    return apiRequest('/admin/api/proxy-pool/refresh', { method: 'POST', signal })
  },
}
