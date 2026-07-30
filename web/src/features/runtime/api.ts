import { apiRequest } from '../../shared/api/client'
import type { RuntimeSettings, RuntimeSettingsResponse, RuntimeSummaryResponse } from './types'

export interface RuntimeApi {
  getSummary(): Promise<RuntimeSummaryResponse>
  getSettings(): Promise<RuntimeSettingsResponse>
  updateSettings(settings: RuntimeSettings): Promise<RuntimeSettingsResponse>
}

export const runtimeApi: RuntimeApi = {
  getSummary() {
    return apiRequest('/admin/api/runtime/summary')
  },
  getSettings() {
    return apiRequest('/admin/api/settings')
  },
  updateSettings(settings) {
    return apiRequest('/admin/api/settings', { method: 'PATCH', body: settings })
  },
}
