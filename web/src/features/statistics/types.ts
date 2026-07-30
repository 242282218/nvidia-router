export type StatisticsDimension = 'global' | 'model' | 'nvidia_key' | 'access_key'

export interface DailyStatistic {
  day: string
  dimension_type: StatisticsDimension
  dimension_id: string
  request_count: number
  success_count: number
  failure_count: number
  average_duration_ms: number
  average_queue_ms: number
  average_attempts: number
  prompt_tokens: number
  completion_tokens: number
}

export interface RecentError {
  request_id: string
  endpoint: string
  model_id?: string
  nvidia_key_id?: number
  access_key_id?: number
  http_status: number
  error_code: string
  upstream_request_id?: string
  created_at: string
}

export interface DailyStatisticsResponse {
  data: DailyStatistic[]
}

export interface RecentErrorsResponse {
  data: RecentError[]
}
