import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// The Go contract test (internal/httpapi/admin/contract_test.go) pins the exact
// admin API wire shape into testdata/contract_*.json. This spec treats those
// snapshots as the single source of truth: every backend field must be declared
// by the front-end type, and every field the front-end type requires must be
// present. A backend field rename, an added field, or a front-end type drift
// fails here with the offending JSON path (audit R13/R3.1).
function resolveSnapshotsDir(): string {
  // Vitest runs from the web workspace; allow a repo-root invocation too.
  const fromWeb = join(process.cwd(), '..', 'internal', 'httpapi', 'admin', 'testdata')
  if (existsSync(fromWeb)) return fromWeb
  return join(process.cwd(), 'internal', 'httpapi', 'admin', 'testdata')
}

const snapshotsDir = resolveSnapshotsDir()

interface ScalarSpec {
  kind: 'scalar'
  type?: 'boolean' | 'number' | 'string'
  required?: boolean
}

interface ObjectSpec {
  kind: 'object'
  required?: boolean
  fields: Record<string, ShapeSpec>
}

interface ArraySpec {
  kind: 'array'
  required?: boolean
  items: ShapeSpec
}

type ShapeSpec = ScalarSpec | ObjectSpec | ArraySpec

const scalar = (type: ScalarSpec['type'], required = true): ScalarSpec => ({ kind: 'scalar', type, required })

const object = (fields: Record<string, ShapeSpec>, required = true): ObjectSpec => ({ kind: 'object', required, fields })

const array = (items: ShapeSpec, required = true): ArraySpec => ({ kind: 'array', required, items })

const optional = <T extends ShapeSpec>(spec: T): T => ({ ...spec, required: false })

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function assertMatches(label: string, spec: ShapeSpec, value: unknown, path = '$'): void {
  if (value === undefined || value === null) {
    if (spec.required === false) return
    throw new Error(`${path} (${label}): backend did not return a field the frontend type requires`)
  }
  switch (spec.kind) {
    case 'scalar':
      if (spec.type !== undefined && typeof value !== spec.type) {
        throw new Error(`${path} (${label}): expected ${spec.type}, got ${typeof value}`)
      }
      return
    case 'object': {
      if (!isRecord(value)) {
        throw new Error(`${path} (${label}): expected object, got ${typeof value}`)
      }
      for (const [key, child] of Object.entries(spec.fields)) {
        if (!(key in value)) {
          if (child.required === false) continue
          throw new Error(`${path}.${key} (${label}): backend snapshot is missing a field the frontend type requires`)
        }
        assertMatches(label, child, value[key], `${path}.${key}`)
      }
      for (const key of Object.keys(value)) {
        if (!(key in spec.fields)) {
          throw new Error(`${path}.${key} (${label}): backend returns a field the frontend type does not declare`)
        }
      }
      return
    }
    case 'array': {
      if (!Array.isArray(value)) {
        throw new Error(`${path} (${label}): expected array, got ${typeof value}`)
      }
      for (const [index, item] of value.entries()) {
        assertMatches(label, spec.items, item, `${path}[${index}]`)
      }
      return
    }
  }
}

function readSnapshot(name: string): unknown {
  const path = join(snapshotsDir, `contract_${name}.json`)
  try {
    return JSON.parse(readFileSync(path, 'utf8')) as unknown
  } catch (error) {
    throw new Error(`cannot read contract snapshot ${path} — run \`go test ./internal/httpapi/admin -update\` to generate it (${error instanceof Error ? error.message : String(error)})`)
  }
}

// ---- field manifests mirroring the front-end types in src/features/*/types.ts

const runtimeSettings = object({
  queue_capacity: scalar('number'),
  queue_wait_timeout_ms: scalar('number'),
  connect_timeout_ms: scalar('number'),
  first_byte_timeout_ms: scalar('number'),
  nonstream_total_timeout_ms: scalar('number'),
  shutdown_grace_ms: scalar('number'),
  failover_status_codes: scalar('string'),
  request_log_retention_days: scalar('number'),
  max_attempts_per_request: scalar('number'),
  retry_budget_ms: scalar('number'),
  max_streaming_per_key: scalar('number'),
  stream_first_token_timeout_ms: scalar('number'),
  stream_idle_timeout_ms: scalar('number'),
  latency_routing_enabled: scalar('boolean'),
  embedding_cache_enabled: scalar('boolean'),
  embedding_cache_max_entries: scalar('number'),
})

const model = object({
  id: scalar('number'),
  public_id: scalar('string'),
  upstream_id: scalar('string'),
  display_name: scalar('string'),
  kind: scalar('string'),
  enabled: scalar('boolean'),
  supports_vision: scalar('boolean'),
  supports_tools: scalar('boolean'),
  supports_reasoning: scalar('boolean'),
  reasoning_wire_format: optional(scalar('string')),
  capability_verified_at: optional(scalar('string')),
  input_usd_per_mtok: optional(scalar('number')),
  output_usd_per_mtok: optional(scalar('number')),
  blocked_by_key_ids: optional(array(scalar('number'))),
})

const candidate = object({
  upstream_id: scalar('string'),
  display_name: scalar('string'),
  kind: scalar('string'),
  supports_vision: scalar('boolean'),
  supports_tools: scalar('boolean'),
  supports_reasoning: scalar('boolean'),
  reasoning_wire_format: optional(scalar('string')),
})

const accessKey = object({
  id: scalar('number'),
  name: scalar('string'),
  key_prefix: scalar('string'),
  created_at: scalar('string'),
  last_used_at: optional(scalar('string')),
  revoked_at: optional(scalar('string')),
  expires_at: optional(scalar('string')),
  rpm_limit: scalar('number'),
  tpm_limit: scalar('number'),
  max_concurrent: scalar('number'),
  token_budget: scalar('number'),
  consumed_tokens: scalar('number'),
})

const nvidiaKey = object({
  id: scalar('number'),
  masked: scalar('string'),
  enabled: scalar('boolean'),
  auth_invalid: scalar('boolean'),
  cooldown_until: optional(scalar('string')),
  cooldown_reason: optional(scalar('string')),
  cooldown_level: scalar('number'),
  consecutive_failures: scalar('number'),
  last_success_at: optional(scalar('string')),
  last_error_at: optional(scalar('string')),
  last_error_code: optional(scalar('string')),
  created_at: scalar('string'),
  updated_at: scalar('string'),
})

const proxyPoolSettings = object({
  enabled: scalar('boolean'),
  proxy_url: scalar('string'),
  auth_configured: scalar('boolean'),
  source: scalar('string'),
})

const runtimeSummary = object({
  keys: object({
    total: scalar('number'),
    enabled: scalar('number'),
    disabled: scalar('number'),
    auth_invalid: scalar('number'),
    cooling_down: scalar('number'),
    ready: scalar('number'),
  }),
  active: scalar('number'),
  queue: object({
    length: scalar('number'),
    capacity: scalar('number'),
  }),
  earliest_cooldown: optional(scalar('string')),
  shutting_down: scalar('boolean'),
})

const monitoringSummary = object({
  request_count: scalar('number'),
  success_count: scalar('number'),
  failure_count: scalar('number'),
  success_rate: scalar('number'),
  average_duration_ms: scalar('number'),
  average_first_byte_ms: scalar('number'),
  average_first_token_ms: scalar('number'),
  average_queue_ms: scalar('number'),
  total_attempts: scalar('number'),
  prompt_tokens: scalar('number'),
  completion_tokens: scalar('number'),
  first_token_p50_ms: optional(scalar('number')),
  first_token_p95_ms: optional(scalar('number')),
})

const monitoringSeriesPoint = object({
  bucket: scalar('string'),
  request_count: scalar('number'),
  success_count: scalar('number'),
  failure_count: scalar('number'),
  average_duration_ms: scalar('number'),
  average_first_byte_ms: scalar('number'),
  average_first_token_ms: scalar('number'),
  average_queue_ms: scalar('number'),
  total_attempts: scalar('number'),
  prompt_tokens: scalar('number'),
  completion_tokens: scalar('number'),
})

const requestLog = object({
  request_id: scalar('string'),
  endpoint: scalar('string'),
  model_id: optional(scalar('string')),
  access_key_id: optional(scalar('number')),
  nvidia_key_id: optional(scalar('number')),
  http_status: scalar('number'),
  outcome: scalar('string'),
  error_code: optional(scalar('string')),
  is_stream: scalar('boolean'),
  queue_ms: scalar('number'),
  first_byte_ms: optional(scalar('number')),
  first_token_ms: optional(scalar('number')),
  duration_ms: scalar('number'),
  attempt_count: scalar('number'),
  prompt_tokens: optional(scalar('number')),
  completion_tokens: optional(scalar('number')),
  upstream_request_id: optional(scalar('string')),
  created_at: scalar('string'),
})

const cases: Array<{ snapshot: string; typeName: string; payload: ShapeSpec }> = [
  { snapshot: 'settings_get', typeName: 'RuntimeSettings', payload: object({ data: runtimeSettings }) },
  { snapshot: 'settings_patch', typeName: 'RuntimeSettings', payload: object({ data: runtimeSettings }) },
  { snapshot: 'models_get', typeName: 'ModelsResponse', payload: object({ data: array(model) }) },
  { snapshot: 'models_candidates_get', typeName: 'CandidatesResponse', payload: object({ data: array(candidate) }) },
  { snapshot: 'models_post', typeName: 'SaveResult', payload: object({ saved: scalar('number') }) },
  { snapshot: 'models_patch', typeName: 'Model', payload: model },
  { snapshot: 'access_keys_get', typeName: 'AccessKeysResponse', payload: object({ data: array(accessKey) }) },
  {
    snapshot: 'access_keys_post',
    typeName: 'CreatedAccessKey',
    payload: object({ ...accessKey.fields, key: scalar('string') }),
  },
  { snapshot: 'nvidia_keys_get', typeName: 'NVIDIAKeysResponse', payload: object({ data: array(nvidiaKey) }) },
  { snapshot: 'proxy_pool_get', typeName: 'ProxyPoolResponse', payload: object({ data: proxyPoolSettings }) },
  { snapshot: 'runtime_summary_get', typeName: 'RuntimeSummaryResponse', payload: object({ data: runtimeSummary }) },
  {
    snapshot: 'monitoring_summary_get',
    typeName: 'MonitoringSnapshot',
    payload: object({
      data: object({
        range: scalar('string'),
        from: scalar('string'),
        to: scalar('string'),
        summary: monitoringSummary,
        series: array(monitoringSeriesPoint),
      }),
    }),
  },
  {
    snapshot: 'monitoring_logs_get',
    typeName: 'RequestLogsPage',
    payload: object({
      data: object({
        items: array(requestLog),
        page: scalar('number'),
        page_size: scalar('number'),
        total: scalar('number'),
        has_more: scalar('boolean'),
      }),
    }),
  },
]

describe('admin API contract snapshots', () => {
  for (const test of cases) {
    it(`${test.typeName} fully parses the ${test.snapshot} snapshot`, () => {
      const payload = readSnapshot(test.snapshot)
      expect(() => assertMatches(test.typeName, test.payload, payload)).not.toThrow()
    })
  }
})
