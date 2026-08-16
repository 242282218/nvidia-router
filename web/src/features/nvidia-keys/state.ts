import { formatDate } from '../../shared/format'
import type { NVIDIAKey } from './types'

/** NVIDIA-key display state shared by the desktop table and mobile cards. */

export { formatDate }

export function keyState(key: NVIDIAKey): { label: string; variant: 'success' | 'warning' | 'danger' | 'muted' } {
  if (key.auth_invalid) return { label: '认证失效', variant: 'danger' }
  if (key.cooldown_until) return { label: key.cooldown_reason || '冷却中', variant: 'warning' }
  return key.enabled ? { label: '启用', variant: 'success' } : { label: '停用', variant: 'muted' }
}
