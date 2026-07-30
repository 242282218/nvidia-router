export interface OpenAIError {
  code: string | null
  message: string
  param: string | null
  type: string
}

export interface OpenAIErrorResponse {
  error: OpenAIError
}

export type ApiMethod = 'DELETE' | 'GET' | 'HEAD' | 'PATCH' | 'POST' | 'PUT'

export interface ApiRequestOptions {
  body?: unknown
  headers?: HeadersInit
  method?: ApiMethod
  signal?: AbortSignal
}
