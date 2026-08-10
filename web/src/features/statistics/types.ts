export type MonitoringRange = '24h' | '7d' | '30d'

export interface MonitoringFilter {
  search?: string
  model_id?: string
  endpoint?: string
  outcome?: 'success' | 'failure'
  status?: number
  access_key_id?: number
  nvidia_key_id?: number
}

export interface MonitoringSeriesPoint {
  bucket: string
  request_count: number
  success_count: number
  failure_count: number
  average_duration_ms: number
  average_first_byte_ms: number
  average_first_token_ms: number
  average_queue_ms: number
  total_attempts: number
  prompt_tokens: number
  completion_tokens: number
}

export interface MonitoringSummary {
  request_count: number
  success_count: number
  failure_count: number
  success_rate: number
  average_duration_ms: number
  average_first_byte_ms: number
  average_first_token_ms: number
  average_queue_ms: number
  total_attempts: number
  prompt_tokens: number
  completion_tokens: number
  first_token_p50_ms?: number
  first_token_p95_ms?: number
}

export interface MonitoringSnapshot {
  range: MonitoringRange
  from: string
  to: string
  summary: MonitoringSummary
  series: MonitoringSeriesPoint[]
}

export interface RequestLog {
  request_id: string
  endpoint: string
  model_id?: string
  access_key_id?: number
  nvidia_key_id?: number
  http_status: number
  outcome: 'success' | 'failure'
  error_code?: string
  is_stream: boolean
  queue_ms: number
  first_byte_ms?: number
  first_token_ms?: number
  duration_ms: number
  attempt_count: number
  prompt_tokens?: number
  completion_tokens?: number
  upstream_request_id?: string
  created_at: string
}

export interface RequestLogsPage {
  items: RequestLog[]
  page: number
  page_size: number
  total: number
  has_more: boolean
}
