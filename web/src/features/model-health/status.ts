import type { ModelHealthModel } from './types'

export type ModelHealthDisplayStatus = 'healthy' | 'degraded' | 'unavailable' | 'unchecked'

type ModelHealthStatusInput = Pick<ModelHealthModel, 'status' | 'probe_count' | 'skipped_count' | 'success_count' | 'failure_count' | 'timeout_count' | 'success_rate'>

export function displayStatus(model: ModelHealthStatusInput): ModelHealthDisplayStatus {
  const effectiveProbeCount = model.success_count + model.failure_count + model.timeout_count
  if (
    model.status === 'unchecked'
    || model.status === 'stale'
    || model.status === 'unconfigured'
    || model.probe_count <= model.skipped_count
    || effectiveProbeCount <= 0
    || !Number.isFinite(model.success_rate)
  ) {
    return 'unchecked'
  }
  if (model.success_rate < 50) return 'unavailable'
  if (model.success_rate < 85) return 'degraded'
  return 'healthy'
}
