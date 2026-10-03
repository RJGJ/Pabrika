import { api } from './client'
import type { CreateTokenInput, Envelope, Token, TokenCreated } from './types'

export const tokens = {
  list: async (signal?: AbortSignal) => (await api.get<Envelope<Token>>('/tokens', { signal })).items,
  /** `project_id` is the project's ULID, never its key. */
  create: (input: CreateTokenInput) => api.post<TokenCreated>('/tokens', input),
  revoke: (id: string) => api.delete(`/tokens/${id}`),
}
