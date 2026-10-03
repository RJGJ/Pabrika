import { api } from './client'
import type { Envelope, Label } from './types'

const base = (key: string) => `/projects/${encodeURIComponent(key)}/labels`

export const labels = {
  list: async (key: string, signal?: AbortSignal) => (await api.get<Envelope<Label>>(base(key), { signal })).items,
  create: (key: string, name: string, color: string) => api.post<Label>(base(key), { name, color }),
  update: (id: string, patch: { name?: string; color?: string }) => api.patch<Label>(`/labels/${id}`, patch),
  remove: (id: string) => api.delete(`/labels/${id}`),
}
