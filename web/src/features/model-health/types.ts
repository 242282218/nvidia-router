export type ModelHealthRange = '1h' | '6h' | '24h' | '7d'
export type ModelHealthGroup = 'default' | 'provider' | 'kind'
export type ModelHealthSort = 'quality' | 'availability' | 'latency' | 'volume' | 'name' | 'recent'
export type ModelHealthStatus = 'healthy' | 'degraded' | 'unavailable' | 'unchecked' | 'stale' | 'unconfigured' | string
export type ModelHealthOutcome = 'success' | 'failure' | 'timeout' | 'skipped' | 'canceled' | 'mixed' | 'empty' | string

export interface ModelHealthSettings {
  enabled: boolean
  interval_seconds: number
  concurrency: number
  updated_at?: string
}

export interface ModelHealthBucket {
  start: string
  end: string
  outcome: ModelHealthOutcome
  probe_count: number
  success_count: number
  failure_count: number
  timeout_count: number
  average_duration_ms: number
}

export interface ModelHealthModel {
  model_id: number
  public_id: string
  display_name: string
  kind: string
  provider: string
  enabled: boolean
  status: ModelHealthStatus
  success_rate: number
  probe_count: number
  success_count: number
  failure_count: number
  timeout_count: number
  skipped_count: number
  last_probe_at?: string
  last_duration_ms?: number
  last_error_code?: string
  consecutive_failures: number
  buckets: ModelHealthBucket[]
}

export interface ModelHealthSummary {
  range: ModelHealthRange
  from: string
  to: string
  models: ModelHealthModel[]
  total_models: number
  healthy_count: number
  degraded_count: number
  unavailable_count: number
  unchecked_count: number
  stale_count: number
  unconfigured_count: number
  settings: ModelHealthSettings
}

export interface ModelHealthSummaryResponse {
  data: ModelHealthSummary
}

export interface ModelHealthSettingsResponse {
  data: ModelHealthSettings
}

export interface ModelHealthRunResponse {
  data: { accepted: boolean }
}
