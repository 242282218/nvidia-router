export type ImportStatus =
  | 'imported'
  | 'duplicate'
  | 'invalid'
  | 'temporarily_unavailable'
  | 'indeterminate'

export interface NVIDIAKey {
  id: number
  masked: string
  enabled: boolean
  auth_invalid: boolean
  cooldown_until?: string
  cooldown_reason?: string
  cooldown_level: number
  consecutive_failures: number
  last_success_at?: string
  last_error_at?: string
  last_error_code?: string
  created_at: string
  updated_at: string
}

export interface ImportResult {
  line?: number
  status: ImportStatus | string
  reason?: string
  masked: string
  key?: NVIDIAKey
}

export interface KeyTestResult {
  id: number
  status: string
  reason?: string
  request_id?: string
  models?: string[]
}

export interface NVIDIAKeysResponse {
  data: NVIDIAKey[]
}

export type SingleImportResponse = ImportResult

export interface BatchImportResponse {
  data: ImportResult[]
}
