export type ProxyPoolSource = 'database' | 'environment' | 'none'

export interface ProxyPoolSettings {
  enabled: boolean
  proxy_url: string
  auth_configured: boolean
  source: ProxyPoolSource
}

export interface ProxyPoolResponse {
  data: ProxyPoolSettings
}

export interface ProxyPoolPatch {
  enabled: boolean
  proxy_url: string
  auth_key: string
  clear_auth_key?: boolean
}

/** Live quality projection of one pooled exit proxy. */
export interface PoolProxyStatus {
  address: string
  latency_ewma_ms: number
  remaining_seconds: number
  healthy: boolean
  ejected: boolean
  success_count: number
  failure_count: number
  /** Consecutive 429/5xx through this exit since the last real 2xx (0 when clean). */
  http_fail_count: number
}

export interface PoolStatusData {
  total_size: number
  healthy_size: number
  proxies: PoolProxyStatus[]
  /** Collector diagnostics; empty strings when not in pool mode. */
  last_fetch_at: string
  last_success_at: string
  /** Provider error code ("403"/"208") or "transport"; empty when healthy. */
  last_error_code: string
}

export interface PoolStatusResponse {
  data: PoolStatusData
}
