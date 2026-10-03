import { api, listAll } from './client'
import type { CreateTicketInput, MoveBody, MoveResult, Ticket, TicketSummary, UpdateTicketInput } from './types'

const enc = encodeURIComponent

export const tickets = {
  listAll: (key: string, signal?: AbortSignal) => listAll<TicketSummary>(`/projects/${enc(key)}/tickets`, {}, signal),
  create: (key: string, input: CreateTicketInput) => api.post<Ticket>(`/projects/${enc(key)}/tickets`, input),
  /** `idOrRef` is a ULID or a reference such as WEB-12. */
  get: (idOrRef: string, signal?: AbortSignal) => api.get<Ticket>(`/tickets/${enc(idOrRef)}`, { signal }),
  update: (id: string, patch: UpdateTicketInput) => api.patch<Ticket>(`/tickets/${enc(id)}`, patch),
  remove: (id: string) => api.delete(`/tickets/${enc(id)}`),
  move: (id: string, body: MoveBody) => api.post<MoveResult>(`/tickets/${enc(id)}/move`, body),
}
