import { apiRequest } from '../../shared/api/client'
import type { DailyStatisticsResponse, RecentErrorsResponse } from './types'

export interface StatisticsApi {
  getDaily(days: number): Promise<DailyStatisticsResponse>
  getRecentErrors(limit: number): Promise<RecentErrorsResponse>
}

export const statisticsApi: StatisticsApi = {
  getDaily(days) {
    return apiRequest(`/admin/api/stats?days=${days}`)
  },
  getRecentErrors(limit) {
    return apiRequest(`/admin/api/errors?limit=${limit}`)
  },
}
