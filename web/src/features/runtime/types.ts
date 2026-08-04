export interface RuntimeKeyCounts {
  total: number
  enabled: number
  disabled: number
  auth_invalid: number
  cooling_down: number
  ready: number
}

export interface RuntimeSummary {
  keys: RuntimeKeyCounts
  active: number
  queue: {
    length: number
    capacity: number
  }
  earliest_cooldown?: string
  shutting_down: boolean
}

export interface RuntimeSettings {
  queue_capacity: number
  queue_wait_timeout_ms: number
  connect_timeout_ms: number
  first_byte_timeout_ms: number
  nonstream_total_timeout_ms: number
  shutdown_grace_ms: number
  failover_status_codes: string
  request_log_retention_days: number
  max_attempts_per_request: number
  retry_budget_ms: number
}

export interface RuntimeSummaryResponse {
  data: RuntimeSummary
}

export interface RuntimeSettingsResponse {
  data: RuntimeSettings
}
