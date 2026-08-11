export interface LiveRequestEvent {
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
  created_at: string
}
