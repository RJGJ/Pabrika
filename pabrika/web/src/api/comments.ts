import { api, listAll } from './client'
import type { Activity, Comment, Envelope } from './types'

export const comments = {
  listAll: (ticketId: string, signal?: AbortSignal) => listAll<Comment>(`/tickets/${ticketId}/comments`, {}, signal),
  add: (ticketId: string, body: string) => api.post<Comment>(`/tickets/${ticketId}/comments`, { body }),
  update: (id: string, body: string) => api.patch<Comment>(`/comments/${id}`, { body }),
  remove: (id: string) => api.delete(`/comments/${id}`),
  /** One page of activity, newest first. */
  activity: (ticketId: string, cursor?: string | null, signal?: AbortSignal) =>
    api.get<Envelope<Activity>>(`/tickets/${ticketId}/activity`, {
      query: { limit: 50, cursor: cursor ?? undefined },
      signal,
    }),
}
