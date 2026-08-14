export type ModelKind = 'chat' | 'embedding' | 'asr' | 'tts' | string

export interface ModelCapabilities {
  supports_vision: boolean
  supports_tools: boolean
  supports_reasoning: boolean
  reasoning_wire_format?: string
}

export interface Candidate extends ModelCapabilities {
  upstream_id: string
  display_name: string
  kind: ModelKind
}

export interface Model extends Candidate {
  id: number
  provider?: string
  public_id: string
  enabled: boolean
  capability_verified_at?: string
  // Per-model overrides of the global streaming windows; null/undefined means
  // "use the global setting".
  stream_first_token_timeout_ms?: number
  stream_idle_timeout_ms?: number
  input_usd_per_mtok?: number
  output_usd_per_mtok?: number
  blocked_by_key_ids?: number[]
}

export interface SaveSelection extends Candidate {
  public_id: string
  enabled: boolean
}

export interface ModelPatch {
  provider?: string
  display_name?: string
  kind?: ModelKind
  enabled?: boolean
  supports_vision?: boolean
  supports_tools?: boolean
  supports_reasoning?: boolean
  reasoning_wire_format?: string
  stream_first_token_timeout_ms?: number
  stream_idle_timeout_ms?: number
  input_usd_per_mtok?: number
  output_usd_per_mtok?: number
}

export interface CandidatesResponse {
  data: Candidate[]
}

export interface ModelsResponse {
  data: Model[]
}
