export interface AccessKey {
  id: number
  name: string
  key_prefix: string
  created_at: string
  last_used_at?: string
  revoked_at?: string
}

export interface CreatedAccessKey extends AccessKey {
  key: string
}

export interface AccessKeysResponse {
  data: AccessKey[]
}
