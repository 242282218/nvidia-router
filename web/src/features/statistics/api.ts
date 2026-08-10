import { apiRequest } from '../../shared/api/client'
import type {
  MonitoringFilter,
  MonitoringRange,
  MonitoringSnapshot,
  RequestLogsPage,
} from './types'

export interface StatisticsApi {
  getSummary(range: MonitoringRange, filter?: MonitoringFilter, signal?: AbortSignal): Promise<{ data: MonitoringSnapshot }>
  getLogs(range: MonitoringRange, filter?: MonitoringFilter, page?: number, pageSize?: number, signal?: AbortSignal): Promise<{ data: RequestLogsPage }>
}

export const statisticsApi: StatisticsApi = {
  getSummary(range, filter = {}, signal) {
    return apiRequest(`/admin/api/monitoring/summary?${monitoringQuery(range, filter)}`, { signal })
  },
  getLogs(range, filter = {}, page = 1, pageSize = 50, signal) {
    const query = monitoringQuery(range, filter)
    query.set('page', String(page))
    query.set('page_size', String(pageSize))
    return apiRequest(`/admin/api/monitoring/logs?${query.toString()}`, { signal })
  },
}

function monitoringQuery(range: MonitoringRange, filter: MonitoringFilter): URLSearchParams {
  const query = new URLSearchParams()
  query.set('range', range)
  for (const key of ['search', 'model_id', 'endpoint', 'outcome', 'status', 'access_key_id', 'nvidia_key_id'] as const) {
    const value = filter[key]
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  return query
}
