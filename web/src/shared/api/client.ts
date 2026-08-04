import type { ApiRequestOptions, OpenAIError, OpenAIErrorResponse } from './types'


export const SESSION_EXPIRED_EVENT = 'session-expired'

export class ApiError extends Error {
  readonly code: string | null
  readonly param: string | null
  readonly retryAfter: string | null
  readonly status: number
  readonly type: string

  constructor(status: number, error: OpenAIError, retryAfter: string | null = null) {
    super(error.message)
    this.name = 'ApiError'
    this.status = status
    this.type = error.type
    this.code = error.code
    this.param = error.param
    this.retryAfter = retryAfter
  }
}

export async function apiRequest<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers = new Headers(options.headers)
  const body = options.body === undefined ? undefined : JSON.stringify(options.body)

  if (body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, {
    body,
    credentials: 'same-origin',
    headers,
    method,
    signal: options.signal,
  })

  if (!response.ok) {
    // Not every 401 invalidates the active session. The login form returns
    // 401 invalid_credentials when the username/password is wrong even
    // while the requester holds a valid cookie, and change-password returns
    // the same status when the current password is mistyped. Auto-bouncing
    // to /login there would kick out a still-authenticated administrator;
    // only treat the explicit invalid_session / session_not_found codes as
    // session-expired so the auth forms can surface the rejection inline.
    const apiErr = await parseApiError(response)
    if (
      response.status === 401 &&
      (apiErr.code === 'invalid_session' || apiErr.code === 'session_not_found')
    ) {
      window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT))
    }
    throw apiErr
  }
  if (response.status === 204) {
    return undefined as T
  }

  return (await response.json()) as T
}

async function parseApiError(response: Response): Promise<ApiError> {
  const payload = await readJson(response)
  if (isOpenAIErrorResponse(payload)) {
    return new ApiError(response.status, payload.error, response.headers.get('Retry-After'))
  }

  return new ApiError(response.status, {
    code: 'http_error',
    message: response.statusText || '请求失败，请稍后重试。',
    param: null,
    type: 'api_error',
  })
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json()
  } catch {
    return null
  }
}

function isOpenAIErrorResponse(value: unknown): value is OpenAIErrorResponse {
  if (!isRecord(value) || !isRecord(value.error)) {
    return false
  }
  const { code, message, param, type } = value.error
  return (
    (typeof code === 'string' || code === null) &&
    typeof message === 'string' &&
    (typeof param === 'string' || param === null) &&
    typeof type === 'string'
  )
}

export function isDataArrayResponse<T>(
  value: unknown,
  isItem: (item: unknown) => item is T,
): value is { data: T[] } {
  return isRecord(value) && Array.isArray(value.data) && value.data.every(isItem)
}

export function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

export function isAbortError(value: unknown): boolean {
  return isRecord(value) && value.name === 'AbortError'
}
