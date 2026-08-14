export type ProxyPoolSource = 'database' | 'environment' | 'none'
export type ProxyPoolMode = 'none' | 'built-in' | string

export interface ProxyPoolSettings {
  enabled: boolean
  proxy_url: string
  auth_configured: boolean
  source: ProxyPoolSource
  mode?: ProxyPoolMode
  upstream_configured?: boolean
  upstream_endpoint?: string
  collector_interval: string
  proxy_ttl: string
  validation_url: string
  validation_status: number
  expected_qty: number
  concurrency: number
  max_latency: string
}

export interface ProxyPoolResponse {
  data: ProxyPoolSettings
}

export interface ProxyPoolPatch {
  enabled: boolean
  upstream_url?: string
  validation_url: string
  validation_status: number
  interval: string
  proxy_ttl: string
  expected_qty: number
  concurrency: number
  max_latency: string
}

export interface PoolStatusData {
  configured?: boolean
  mode?: ProxyPoolMode
  endpoint?: string
  reachable?: boolean
  health_latency_ms?: number
  // Detailed per-exit quality is rendered by the built-in operations table.
  total_size?: number
  healthy_size?: number
  collector_enabled?: boolean
  panic_mode?: boolean
  upstream_overloaded?: boolean
  last_upstream_overload_at?: string
  proxies?: ProxyStatus[]
  last_fetch_at?: string
  last_success_at?: string
  last_error_code?: string
}

export interface ProxyStatus {
  address: string
  healthy?: boolean
  ejected?: boolean
  latency_ewma_ms?: number
  quality_score?: number
  remaining_seconds?: number
}

export interface PoolStatusResponse {
  data: PoolStatusData
}
