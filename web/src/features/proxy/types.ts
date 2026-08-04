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
