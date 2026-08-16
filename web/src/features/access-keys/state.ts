import { formatDate } from '../../shared/format'
import type { AccessKey } from './types'

/** Access-key display state shared by the desktop table and mobile cards.
 * Duplicating this per component is how the two views drifted apart
 * pre-refactor. */

export function formatKeyValue(value?: string): string {
  return value ? formatDate(value) : '从未使用'
}

export function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(value)
}

/** Fraction of a key's token budget already consumed (0..100). Unlimited
 * keys (budget 0) show no meter. */
export function budgetUsagePercent(key: AccessKey): number {
  if (key.token_budget <= 0) return 0
  return Math.min(100, Math.round((key.consumed_tokens / key.token_budget) * 100))
}

export function isExpired(key: AccessKey): boolean {
  if (!key.expires_at) return false
  const expiry = new Date(key.expires_at)
  return !Number.isNaN(expiry.getTime()) && expiry.getTime() <= Date.now()
}

export function isBudgetExhausted(key: AccessKey): boolean {
  return key.token_budget > 0 && key.consumed_tokens >= key.token_budget
}

/** Operator-facing state with a fixed precedence: revoked > expired >
 * budget exhausted > valid. A key can be simultaneously expired and out of
 * budget; the first condition wins so the UI never claims a refused key is
 * usable. */
export function keyState(key: AccessKey): { label: string; variant: 'success' | 'warning' | 'danger' | 'muted' } {
  if (key.revoked_at) return { label: '已撤销', variant: 'muted' }
  if (isExpired(key)) return { label: '已过期', variant: 'warning' }
  if (isBudgetExhausted(key)) return { label: '预算已耗尽', variant: 'danger' }
  return { label: '有效', variant: 'success' }
}
