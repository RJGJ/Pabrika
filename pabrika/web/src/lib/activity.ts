import type { Activity, Member } from '@/api/types'

const STATUS_NAMES: Record<string, string> = {
  backlog: 'Backlog',
  todo: 'To do',
  in_progress: 'In progress',
  done: 'Done',
}
export const statusName = (s: unknown) => (typeof s === 'string' ? (STATUS_NAMES[s] ?? s) : String(s))

type Pair = [unknown, unknown]
const isPair = (v: unknown): v is Pair => Array.isArray(v) && v.length === 2

function userName(id: unknown, members: Member[]): string {
  if (typeof id !== 'string') return 'someone'
  return members.find((m) => m.user.id === id)?.user.display_name ?? 'someone'
}

function nameList(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
}

function describeField(field: string, value: unknown, action: string, members: Member[]): string | null {
  if (field === 'position') return null
  if (!isPair(value)) return `updated ${field}`
  const [from, to] = value
  switch (field) {
    case 'status':
      return action === 'moved'
        ? `moved from ${statusName(from)} to ${statusName(to)}`
        : `changed status from ${statusName(from)} to ${statusName(to)}`
    case 'priority':
      return `changed priority from ${String(from)} to ${String(to)}`
    case 'title':
      return `renamed the ticket from "${String(from)}" to "${String(to)}"`
    case 'description':
      return 'changed the description'
    case 'due_date':
      return to ? `set the due date to ${String(to)}` : 'cleared the due date'
    case 'assignee':
      if (!from && to) return `assigned to ${userName(to, members)}`
      if (from && !to) return `unassigned ${userName(from, members)}`
      return `reassigned from ${userName(from, members)} to ${userName(to, members)}`
    case 'labels': {
      const before = nameList(from)
      const after = nameList(to)
      const added = after.filter((n) => !before.includes(n))
      const removed = before.filter((n) => !after.includes(n))
      const parts: string[] = []
      if (added.length) parts.push(`added label${added.length > 1 ? 's' : ''} ${added.join(', ')}`)
      if (removed.length) parts.push(`removed label${removed.length > 1 ? 's' : ''} ${removed.join(', ')}`)
      return parts.length ? parts.join(' and ') : 'updated labels'
    }
    default:
      return `updated ${field}`
  }
}

/** Human text for an activity row, without the actor (the UI renders the actor separately). */
export function humanizeActivity(a: Activity, members: Member[]): string {
  if (a.action === 'created') return 'created the ticket'
  if (a.action === 'deleted') return 'deleted the ticket'
  const changes = a.changes && typeof a.changes === 'object' ? a.changes : {}
  const fields = Object.keys(changes)
  const parts = fields
    .map((f) => describeField(f, (changes as Record<string, unknown>)[f], a.action, members))
    .filter((p): p is string => p !== null)
  if (parts.length > 0) return parts.join(' and ')
  if (fields.length > 0 && fields.every((f) => f === 'position')) return 'reordered the ticket'
  return 'updated the ticket'
}
