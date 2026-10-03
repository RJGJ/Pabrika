import type { Role, Status, TicketSummary } from '@/api/types'
import { STATUSES } from '@/api/types'
import { useBoardStore } from '@/stores/board'
import { member, project } from '../stores/helpers'

/** Put the board store into a ready state without any network. */
export function seedBoard(
  tickets: TicketSummary[],
  opts: { role?: Role; archived?: boolean } = {},
): ReturnType<typeof useBoardStore> {
  const board = useBoardStore()
  board.project = project({ role: opts.role ?? 'owner', archived_at: opts.archived ? '2026-01-01T00:00:00Z' : null })
  board.members = [member('u-me', 'Me'), member('u2', 'Bob')]
  board.labels = []
  const byId: Record<string, TicketSummary> = {}
  const cols: Record<Status, string[]> = { backlog: [], todo: [], in_progress: [], done: [] }
  for (const t of tickets) {
    byId[t.id] = t
    cols[t.status].push(t.id)
  }
  board.ticketsById = byId
  board.columns = cols
  board.ticketsLoaded = true
  board.loadState = 'ready'
  void STATUSES
  return board
}
