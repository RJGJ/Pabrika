import type { Label, Member, Priority, TicketSummary } from '@/api/types'
import { PRIORITIES } from '@/api/types'

export interface BoardFilters {
  q: string
  priority: Priority[]
  /** 'me', 'none' (unassigned) or a user id; null means anyone. */
  assignee: string | null
  label: string[]
}

export const emptyFilters = (): BoardFilters => ({ q: '', priority: [], assignee: null, label: [] })

export const FILTER_KEYS = ['q', 'priority', 'assignee', 'label'] as const

type QueryValue = string | null | undefined | (string | null)[]
export type QueryLike = Record<string, QueryValue>

const first = (v: QueryValue): string => (Array.isArray(v) ? (v[0] ?? '') : (v ?? '')) || ''
const csv = (v: QueryValue): string[] =>
  first(v)
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)

/**
 * Read filters from a route query. Unknown priorities are dropped. When `known` is given, unknown
 * assignee and label ids are dropped too (omit it until members and labels have loaded).
 */
export function parseFilters(
  query: QueryLike,
  known?: { members: Pick<Member, 'user'>[]; labels: Pick<Label, 'id'>[] },
): BoardFilters {
  const priority = [...new Set(csv(query.priority).filter((p): p is Priority => (PRIORITIES as readonly string[]).includes(p)))]
  let assignee: string | null = first(query.assignee).trim() || null
  if (assignee && assignee !== 'me' && assignee !== 'none' && known && !known.members.some((m) => m.user.id === assignee)) {
    assignee = null
  }
  let label = [...new Set(csv(query.label))]
  if (known) {
    const ids = new Set(known.labels.map((l) => l.id))
    label = label.filter((id) => ids.has(id))
  }
  return { q: first(query.q).trim(), priority, assignee, label }
}

/** Query entries for the active filters only (inactive keys are absent so they can be removed from the URL). */
export function serializeFilters(f: BoardFilters): Partial<Record<(typeof FILTER_KEYS)[number], string>> {
  const out: Partial<Record<(typeof FILTER_KEYS)[number], string>> = {}
  if (f.q) out.q = f.q
  if (f.priority.length) out.priority = f.priority.join(',')
  if (f.assignee) out.assignee = f.assignee
  if (f.label.length) out.label = f.label.join(',')
  return out
}

export function filtersActive(f: BoardFilters): boolean {
  return !!f.q || f.priority.length > 0 || !!f.assignee || f.label.length > 0
}

export function matchesFilters(t: TicketSummary, f: BoardFilters, meId: string | null): boolean {
  if (f.q) {
    const q = f.q.toLowerCase()
    if (!t.ref.toLowerCase().includes(q) && !t.title.toLowerCase().includes(q)) return false
  }
  if (f.priority.length && !f.priority.includes(t.priority)) return false
  if (f.assignee) {
    if (f.assignee === 'none') {
      if (t.assignee) return false
    } else {
      const want = f.assignee === 'me' ? meId : f.assignee
      if (!want || t.assignee?.id !== want) return false
    }
  }
  if (f.label.length && !t.labels.some((l) => f.label.includes(l.id))) return false
  return true
}
