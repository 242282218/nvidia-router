import { apiRequest } from '../../shared/api/client'
import type {
  Candidate,
  Model,
  ModelPatch,
  ModelsResponse,
  SaveSelection,
  CandidatesResponse,
} from './types'

export interface ModelsApi {
  candidates(): Promise<CandidatesResponse>
  list(): Promise<ModelsResponse>
  save(models: SaveSelection[]): Promise<{ saved: number }>
  patch(id: number, patch: ModelPatch): Promise<Model>
  unblock(keyId: number, modelId: number): Promise<Model>
  delete(id: number): Promise<void>
}

export const modelsApi: ModelsApi = {
  candidates() {
    return apiRequest('/admin/api/models/candidates')
  },
  list() {
    return apiRequest('/admin/api/models')
  },
  save(models) {
    return apiRequest('/admin/api/models', { method: 'POST', body: { models } })
  },
  patch(id, patch) {
    return apiRequest(`/admin/api/models/${id}`, { method: 'PATCH', body: patch })
  },
  unblock(keyId, modelId) {
    return apiRequest(`/admin/api/key-model-blocks/${keyId}/${modelId}`, { method: 'DELETE' })
  },
  delete(id) {
    return apiRequest(`/admin/api/models/${id}`, { method: 'DELETE' })
  },
}

export type { Candidate, Model, ModelPatch, SaveSelection }
