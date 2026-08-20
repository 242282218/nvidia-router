import { apiRequest } from '../../shared/api/client'
import type {
  ModelHealthGroup,
  ModelHealthRange,
  ModelHealthRunResponse,
  ModelHealthSettings,
  ModelHealthSettingsResponse,
  ModelHealthSort,
  ModelHealthSummaryResponse,
} from './types'

export interface ModelHealthApi {
  getSummary(range: ModelHealthRange, group: ModelHealthGroup, sort: ModelHealthSort, signal?: AbortSignal): Promise<ModelHealthSummaryResponse>
  getSettings(signal?: AbortSignal): Promise<ModelHealthSettingsResponse>
  updateSettings(settings: Pick<ModelHealthSettings, 'enabled' | 'interval_seconds' | 'concurrency'>, signal?: AbortSignal): Promise<ModelHealthSettingsResponse>
  runNow(signal?: AbortSignal): Promise<ModelHealthRunResponse>
}

export const modelHealthApi: ModelHealthApi = {
  getSummary(range, group, sort, signal) {
    const query = new URLSearchParams({ range, group, sort })
    return apiRequest(`/admin/api/model-health/summary?${query.toString()}`, { signal })
  },
  getSettings(signal) {
    return apiRequest('/admin/api/model-health/settings', { signal })
  },
  updateSettings(settings, signal) {
    return apiRequest('/admin/api/model-health/settings', { method: 'PATCH', body: settings, signal })
  },
  runNow(signal) {
    return apiRequest('/admin/api/model-health/run', { method: 'POST', signal })
  },
}
