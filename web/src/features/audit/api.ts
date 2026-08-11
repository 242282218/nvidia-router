import { apiRequest } from '../../shared/api/client'
import type { AuditLogsPage } from './types'

export interface AuditApi {
  list(params: { page?: number; pageSize?: number; action?: string; signal?: AbortSignal }): Promise<{ data: AuditLogsPage }>
}

export const auditApi: AuditApi = {
  list({ page = 1, pageSize = 50, action, signal } = {}) {
    const query = new URLSearchParams()
    query.set('limit', String(pageSize))
    query.set('offset', String((page - 1) * pageSize))
    if (action) query.set('action', action)
    return apiRequest(`/admin/api/audit-logs?${query.toString()}`, { signal })
  },
}
