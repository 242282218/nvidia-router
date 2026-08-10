export interface AccessKey {
  id: number
  name: string
  key_prefix: string
  created_at: string
  last_used_at?: string
  revoked_at?: string
  expires_at?: string
  rpm_limit: number
  tpm_limit: number
  max_concurrent: number
}

export interface AccessKeyPolicy {
  expires_at?: string | null
  rpm_limit: number
  tpm_limit: number
  max_concurrent: number
}

export interface CreatedAccessKey extends AccessKey {
  key: string
}

export interface AccessKeysResponse {
  data: AccessKey[]
}
