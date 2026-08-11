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
  public_id: string
  enabled: boolean
  capability_verified_at?: string
  input_usd_per_mtok?: number
  output_usd_per_mtok?: number
  blocked_by_key_ids?: number[]
}

export interface SaveSelection extends Candidate {
  public_id: string
  enabled: boolean
}

export interface ModelPatch {
  display_name?: string
  kind?: ModelKind
  enabled?: boolean
  supports_vision?: boolean
  supports_tools?: boolean
  supports_reasoning?: boolean
  reasoning_wire_format?: string
  input_usd_per_mtok?: number
  output_usd_per_mtok?: number
}

export interface CandidatesResponse {
  data: Candidate[]
}

export interface ModelsResponse {
  data: Model[]
}
