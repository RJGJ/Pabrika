import { api } from './client'
import type { ActivityFeedItem, Envelope } from './types'

export interface FeedQuery {
  limit?: number
  cursor?: string | null
  kind?: string
  actor_type?: 'user' | 'api_token'
  ticket?: string
}

export const activityFeed = {
  /** One page of the project-wide feed, newest first. `key` is the project key or id. */
  list: (key: string, q: FeedQuery = {}, signal?: AbortSignal) =>
    api.get<Envelope<ActivityFeedItem>>(`/projects/${encodeURIComponent(key)}/activity`, {
      query: { limit: q.limit ?? 50, cursor: q.cursor ?? undefined, kind: q.kind, actor_type: q.actor_type, ticket: q.ticket },
      signal,
    }),
}
