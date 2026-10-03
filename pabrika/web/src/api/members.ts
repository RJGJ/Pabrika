import { api } from './client'
import type { Envelope, Member, Role } from './types'

const base = (key: string) => `/projects/${encodeURIComponent(key)}/members`

export const members = {
  list: async (key: string, signal?: AbortSignal) => (await api.get<Envelope<Member>>(base(key), { signal })).items,
  add: (key: string, email: string, role: Role) => api.post<Member>(base(key), { email, role }),
  setRole: (key: string, userId: string, role: Role) => api.patch<Member>(`${base(key)}/${userId}`, { role }),
  remove: (key: string, userId: string) => api.delete(`${base(key)}/${userId}`),
}
