import type { ActivityFeedItem } from '@/api/types'
import { parseRoleTag, TAGGED_ROLES, type TaggedRole } from '@/lib/roleTag'

export type FeedActor = 'all' | 'agent' | 'human'

export interface FeedFilters {
  actor: FeedActor
  role: TaggedRole | null
  /** Case-insensitive part of a ticket reference, for example `WEB-30`. */
  ticket: string
  /** Decisions plus label moves to or from an `awaiting-*` gate label. */
  gates: boolean
}

export const emptyFeedFilters = (): FeedFilters => ({ actor: 'all', role: null, ticket: '', gates: false })
export const FEED_FILTER_KEYS = ['actor', 'role', 'ticket', 'gates'] as const

type QueryValue = string | null | undefined | (string | null)[]
const first = (v: QueryValue): string => (Array.isArray(v) ? (v[0] ?? '') : (v ?? '')) || ''

export function parseFeedFilters(query: Record<string, QueryValue>): FeedFilters {
  const actor = first(query.actor)
  const role = first(query.role)
  return {
    actor: actor === 'agent' || actor === 'human' ? actor : 'all',
    role: (TAGGED_ROLES as readonly string[]).includes(role) ? (role as TaggedRole) : null,
    ticket: first(query.ticket).trim(),
    gates: first(query.gates) === '1',
  }
}

export function serializeFeedFilters(f: FeedFilters): Partial<Record<(typeof FEED_FILTER_KEYS)[number], string>> {
  const out: Partial<Record<(typeof FEED_FILTER_KEYS)[number], string>> = {}
  if (f.actor !== 'all') out.actor = f.actor
  if (f.role) out.role = f.role
  if (f.ticket) out.ticket = f.ticket
  if (f.gates) out.gates = '1'
  return out
}

export const feedFiltersActive = (f: FeedFilters): boolean => f.actor !== 'all' || !!f.role || !!f.ticket || f.gates

function touchesGateLabel(changes: Record<string, unknown> | undefined): boolean {
  const pair = changes?.labels
  if (!Array.isArray(pair) || pair.length !== 2) return false
  return pair.some((side) => Array.isArray(side) && side.some((n) => typeof n === 'string' && n.startsWith('awaiting-')))
    && JSON.stringify([...(pair[0] as string[])].filter((n) => n.startsWith('awaiting-')).sort())
      !== JSON.stringify([...(pair[1] as string[])].filter((n) => n.startsWith('awaiting-')).sort())
}

/** Client-side filters (the server only narrows by actor type). */
export function matchesFeedFilters(item: ActivityFeedItem, f: FeedFilters): boolean {
  if (f.actor === 'agent' && !item.actor.bot) return false
  if (f.actor === 'human' && item.actor.bot) return false
  if (f.ticket && !item.ticket.ref.toLowerCase().includes(f.ticket.toLowerCase())) return false
  if (f.role) {
    if (item.kind !== 'comment') return false
    if (parseRoleTag(item.summary ?? '', item.actor)?.role !== f.role) return false
  }
  if (f.gates) {
    const decision = item.kind === 'decision' || item.action === 'decision'
    if (!decision && !touchesGateLabel(item.changes)) return false
  }
  return true
}
