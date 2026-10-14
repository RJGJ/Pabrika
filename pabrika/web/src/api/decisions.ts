import { api } from './client'
import type { CreateDecisionInput, Decision, Envelope } from './types'

const enc = encodeURIComponent

export const decisions = {
  /** Session only: the server answers 403 `session_required` to tokens. Record-only, never moves the ticket. */
  create: (ticketId: string, input: CreateDecisionInput) =>
    api.post<Decision>(`/tickets/${enc(ticketId)}/decisions`, input),
  /** One page, newest first. */
  list: (ticketId: string, cursor?: string | null, signal?: AbortSignal) =>
    api.get<Envelope<Decision>>(`/tickets/${enc(ticketId)}/decisions`, {
      query: { limit: 50, cursor: cursor ?? undefined },
      signal,
    }),
}
