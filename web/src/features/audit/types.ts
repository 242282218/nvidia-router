export interface AuditEntry {
  id: number
  action: string
  target_type: string
  target_id?: string
  detail?: string
  session_id?: string
  client_ip?: string
  created_at: string
}

export interface AuditLogsPage {
  items: AuditEntry[]
  total: number
  has_more: boolean
  next?: number
}

export const AUDIT_ACTIONS = [
  'nvidia_keys.import',
  'nvidia_keys.import_batch',
  'nvidia_keys.update',
  'nvidia_keys.delete',
  'nvidia_keys.test',
  'nvidia_keys.test_all',
  'access_keys.create',
  'access_keys.update_policy',
  'access_keys.revoke',
  'settings.update',
  'proxy_pool.update',
  'models.verify',
  'models.unblock',
  'auth.login',
  'auth.logout',
  'auth.change-password',
] as const
