import { apiRequest } from '../../shared/api/client'
import type {
  BatchImportResponse,
  ImportResult,
  KeyTestResult,
  NVIDIAKey,
  NVIDIAKeysResponse,
  SingleImportResponse,
} from './types'

export interface NvidiaKeysApi {
  list(): Promise<NVIDIAKeysResponse>
  importOne(key: string): Promise<SingleImportResponse>
  importBatch(keys: string): Promise<BatchImportResponse>
  test(id: number): Promise<KeyTestResult>
  testAll(): Promise<{ data: KeyTestResult[] }>
  setEnabled(id: number, enabled: boolean): Promise<{ id: number; enabled: boolean }>
  remove(id: number): Promise<void>
}

export const nvidiaKeysApi: NvidiaKeysApi = {
  list() {
    return apiRequest('/admin/api/nvidia-keys')
  },
  importOne(key) {
    return apiRequest('/admin/api/nvidia-keys', { method: 'POST', body: { key } })
  },
  importBatch(keys) {
    return apiRequest('/admin/api/nvidia-keys/batch', { method: 'POST', body: { keys } })
  },
  test(id) {
    return apiRequest(`/admin/api/nvidia-keys/${id}/test`, { method: 'POST' })
  },
  testAll() {
    return apiRequest('/admin/api/nvidia-keys/test-all', { method: 'POST' })
  },
  setEnabled(id, enabled) {
    return apiRequest(`/admin/api/nvidia-keys/${id}`, { method: 'PATCH', body: { enabled } })
  },
  remove(id) {
    return apiRequest(`/admin/api/nvidia-keys/${id}`, { method: 'DELETE' })
  },
}

export type { BatchImportResponse, ImportResult, KeyTestResult, NVIDIAKey }
