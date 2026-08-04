import { apiRequest } from '../../shared/api/client'
import type { RuntimeSettings, RuntimeSettingsResponse, RuntimeSummaryResponse } from './types'

export interface RuntimeApi {
  getSummary(signal?: AbortSignal): Promise<RuntimeSummaryResponse>
  getSettings(signal?: AbortSignal): Promise<RuntimeSettingsResponse>
  updateSettings(settings: RuntimeSettings, signal?: AbortSignal): Promise<RuntimeSettingsResponse>
}

export const runtimeApi: RuntimeApi = {
  getSummary(signal) {
    return apiRequest('/admin/api/runtime/summary', { signal })
  },
  getSettings(signal) {
    return apiRequest('/admin/api/settings', { signal })
  },
  updateSettings(settings, signal) {
    return apiRequest('/admin/api/settings', { method: 'PATCH', body: settings, signal })
  },
}
