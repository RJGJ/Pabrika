import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn() } }))
vi.mock('@/api/labels', () => ({ labels: { list: vi.fn() } }))
vi.mock('@/api/tickets', () => ({
  tickets: { listAll: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), move: vi.fn() },
}))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { members as membersApi } from '@/api/members'
import { labels as labelsApi } from '@/api/labels'
import { projects as projectsApi } from '@/api/projects'
import { tickets as ticketsApi } from '@/api/tickets'
import { notify } from '@/lib/toast'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'
import { apiError, deferred, flush, full, label, member, ME, project, ticket, type Mocked } from './helpers'

const P = projectsApi as unknown as Mocked
const M = membersApi as unknown as Mocked
const L = labelsApi as unknown as Mocked
const T = ticketsApi as unknown as Mocked

function seed(list = [ticket(1), ticket(2), ticket(3)]) {
  P.get.mockResolvedValue(project())
  M.list.mockResolvedValue([member('u-me', 'Me'), member('u2', 'Bob')])
  L.list.mockResolvedValue([label('l1', 'bug')])
  T.listAll.mockResolvedValue(list)
}

beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [P, M, L, T]) for (const f of Object.values(m)) f.mockReset()
  ;(notify as unknown as ReturnType<typeof vi.fn>).mockReset()
  useAuthStore().user = ME
})

describe('board.load', () => {
  it('loads project, members, labels and tickets and orders columns by position then id', async () => {
    seed([
      ticket(1, { status: 'todo', position: 2000 }),
      ticket(2, { status: 'todo', position: 1000 }),
      ticket(3, { status: 'todo', position: 1000, id: 'a-first' }),
      ticket(4, { status: 'done' }),
    ])
    const b = useBoardStore()
    await b.load('WEB')
    expect(b.loadState).toBe('ready')
    expect(b.columns.todo).toEqual(['a-first', 't2', 't1'])
    expect(b.columns.done).toEqual(['t4'])
    expect(b.counts.todo).toBe(3)
    expect(b.members).toHaveLength(2)
    expect(P.get).toHaveBeenCalledWith('WEB', expect.anything())
  })

  it('handles summaries without description and strips it from full tickets', async () => {
    seed([ticket(1)])
    const b = useBoardStore()
    await b.load('WEB')
    expect('description' in b.ticketsById.t1).toBe(false)
  })

  it('first-load 404 is not-found', async () => {
    seed()
    P.get.mockRejectedValue(apiError(404, 'not_found'))
    const b = useBoardStore()
    await b.load('NOPE')
    expect(b.loadState).toBe('not-found')
  })

  it('a 404 on a quiet reload of a loaded project is lost access (and removes it from the sidebar)', async () => {
    seed()
    const projects = useProjectsStore()
    projects.list = [{ ...project(), counts: undefined } as never]
    const b = useBoardStore()
    await b.load('WEB')
    P.get.mockRejectedValue(apiError(404, 'not_found'))
    await b.load('WEB', { quiet: true })
    expect(b.loadState).toBe('no-access')
    expect(b.lostAccess).toBe(true)
    expect(projects.list).toHaveLength(0)
  })

  it('leaving suppresses the lost-access path', async () => {
    seed()
    const b = useBoardStore()
    await b.load('WEB')
    b.leaving = true
    P.get.mockRejectedValue(apiError(404, 'not_found'))
    await b.load('WEB', { quiet: true })
    expect(b.lostAccess).toBe(false)
    expect(b.loadState).toBe('ready')
  })

  it('first-load network error is error; a quiet error keeps the UI', async () => {
    seed()
    T.listAll.mockRejectedValue(apiError(0, 'network', 'Network error'))
    const b = useBoardStore()
    await b.load('WEB')
    expect(b.loadState).toBe('error')
    seed()
    await b.load('WEB')
    expect(b.loadState).toBe('ready')
    T.listAll.mockRejectedValue(apiError(0, 'network', 'Network error'))
    await b.load('WEB', { quiet: true })
    expect(b.loadState).toBe('ready')
  })

  it('aborts and discards an in-flight load when the key changes', async () => {
    const slow = deferred<ReturnType<typeof project>>()
    seed()
    P.get.mockImplementationOnce(() => slow.promise)
    const b = useBoardStore()
    const first = b.load('AAA')
    P.get.mockResolvedValue(project({ id: 'p2', key: 'BBB' }))
    T.listAll.mockResolvedValue([ticket(9, { project_id: 'p2' })])
    await b.load('BBB')
    slow.resolve(project({ id: 'p1', key: 'AAA' }))
    await first
    expect(b.project?.key).toBe('BBB')
    expect(b.ticketsById.t9).toBeTruthy()
    const signal = P.get.mock.calls[0][1] as AbortSignal
    expect(signal.aborted).toBe(true)
  })

  it('coalesces a quiet reload requested during a load into one rerun', async () => {
    seed()
    const b = useBoardStore()
    const slow = deferred<ReturnType<typeof project>>()
    P.get.mockImplementationOnce(() => slow.promise)
    const first = b.load('WEB')
    void b.load('WEB', { quiet: true })
    void b.load('WEB', { quiet: true })
    slow.resolve(project())
    await first
    await flush()
    expect(P.get).toHaveBeenCalledTimes(2)
  })

  it('archived projects turn canEdit off; viewers cannot edit', async () => {
    seed()
    P.get.mockResolvedValue(project({ archived_at: '2026-01-01T00:00:00Z' }))
    const b = useBoardStore()
    await b.load('WEB')
    expect(b.isArchived).toBe(true)
    expect(b.canEdit).toBe(false)
    P.get.mockResolvedValue(project({ role: 'viewer' }))
    await b.load('WEB', { quiet: true })
    expect(b.canEdit).toBe(false)
    expect(b.isViewer).toBe(true)
  })

  it('tickets: false skips the ticket list', async () => {
    seed()
    const b = useBoardStore()
    await b.load('WEB', { tickets: false })
    expect(T.listAll).not.toHaveBeenCalled()
    expect(b.loadState).toBe('ready')
  })

  it('a quiet reload requested during a drag is deferred until the drag ends', async () => {
    seed()
    const b = useBoardStore()
    await b.load('WEB')
    b.startDrag('backlog')
    await b.load('WEB', { quiet: true })
    expect(T.listAll).toHaveBeenCalledTimes(1)
    b.endDrag()
    await flush()
    expect(T.listAll).toHaveBeenCalledTimes(2)
  })
})

describe('board filters', () => {
  async function loaded() {
    seed([
      ticket(1, { title: 'Fix login', priority: 'high', assignee: { id: 'u-me', display_name: 'Me', email: '' } }),
      ticket(2, { title: 'Dark mode', priority: 'low', labels: [label('l1')] }),
      ticket(3, { title: 'Other', priority: 'high' }),
    ])
    const b = useBoardStore()
    await b.load('WEB')
    return b
  }
  it('searches, filters by priority, assignee (me, none) and label, and combines', async () => {
    const b = await loaded()
    b.setFilters({ q: 'fix', priority: [], assignee: null, label: [] })
    expect(b.filteredColumns.backlog).toEqual(['t1'])
    expect(b.filteredCounts.backlog).toBe(1)
    expect(b.counts.backlog).toBe(3)
    expect(b.filtersActive).toBe(true)
    b.setFilters({ q: '', priority: ['high'], assignee: 'me', label: [] })
    expect(b.filteredColumns.backlog).toEqual(['t1'])
    b.setFilters({ q: '', priority: [], assignee: 'none', label: [] })
    expect(b.filteredColumns.backlog).toEqual(['t2', 't3'])
    b.setFilters({ q: '', priority: [], assignee: null, label: ['l1'] })
    expect(b.filteredColumns.backlog).toEqual(['t2'])
    b.setFilters({ q: 'dark', priority: ['high'], assignee: null, label: [] })
    expect(b.filteredColumns.backlog).toEqual([])
  })
})

describe('board.moveTicket', () => {
  async function loaded() {
    seed([
      ticket(1, { status: 'backlog', position: 1024 }),
      ticket(2, { status: 'backlog', position: 2048 }),
      ticket(3, { status: 'todo', position: 1024 }),
    ])
    const b = useBoardStore()
    await b.load('WEB')
    return b
  }

  it('applies the move optimistically, sends the placement and adopts the server position', async () => {
    const b = await loaded()
    const d = deferred<unknown>()
    T.move.mockReturnValue(d.promise)
    const done = b.moveTicket('t1', 'todo', { after: 't3' })
    expect(b.columns.todo).toEqual(['t3', 't1'])
    expect(b.columns.backlog).toEqual(['t2'])
    expect(b.pendingMoves.has('t1')).toBe(true)
    expect(T.move).toHaveBeenCalledWith('t1', { status: 'todo', after: 't3' })
    d.resolve({ ...full(ticket(1, { status: 'todo', position: 5000 })), renumbered: false })
    expect(await done).toBe(true)
    expect(b.ticketsById.t1.position).toBe(5000)
    expect(b.pendingMoves.size).toBe(0)
  })

  it('Status select path: place bottom', async () => {
    const b = await loaded()
    T.move.mockResolvedValue({ ...full(ticket(1, { status: 'done', position: 1024 })), renumbered: false })
    await b.moveTicket('t1', 'done', { place: 'bottom' })
    expect(T.move).toHaveBeenCalledWith('t1', { status: 'done', place: 'bottom' })
    expect(b.columns.done).toEqual(['t1'])
  })

  it('rolls back, toasts, bumps keys and quietly reloads on 422', async () => {
    const b = await loaded()
    const keysBefore = { ...b.columnKeys }
    T.move.mockRejectedValue(apiError(422, 'validation_failed', 'neighbor moved'))
    const ok = await b.moveTicket('t1', 'todo', { after: 't3' })
    expect(ok).toBe(false)
    expect(b.columns.backlog).toEqual(['t1', 't2'])
    expect(b.columns.todo).toEqual(['t3'])
    expect(b.ticketsById.t1.status).toBe('backlog')
    expect(b.columnKeys.backlog).toBeGreaterThan(keysBefore.backlog)
    expect(b.columnKeys.todo).toBeGreaterThan(keysBefore.todo)
    expect(notify).toHaveBeenCalledWith('error', expect.stringContaining("Couldn't move WEB-1"))
    await flush()
    expect(T.listAll).toHaveBeenCalledTimes(2)
  })

  it('409 project_archived rolls back and reloads', async () => {
    const b = await loaded()
    T.move.mockRejectedValue(apiError(409, 'project_archived', 'archived'))
    await b.moveTicket('t1', 'todo', { place: 'top' })
    expect(b.columns.backlog).toEqual(['t1', 't2'])
    await flush()
    expect(P.get).toHaveBeenCalledTimes(2)
  })

  it('serializes moves FIFO', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.move.mockReturnValueOnce(d1.promise)
    T.move.mockResolvedValueOnce({ ...full(ticket(2, { status: 'todo', position: 9000 })), renumbered: false })
    const p1 = b.moveTicket('t1', 'todo', { after: 't3' })
    const p2 = b.moveTicket('t2', 'todo', { after: 't1' })
    await flush()
    expect(T.move).toHaveBeenCalledTimes(1)
    d1.resolve({ ...full(ticket(1, { status: 'todo', position: 5000 })), renumbered: false })
    await Promise.all([p1, p2])
    expect(T.move).toHaveBeenCalledTimes(2)
    expect(T.move.mock.calls[1]).toEqual(['t2', { status: 'todo', after: 't1' }])
  })

  it('recomputes a queued move whose neighbor no longer exists in the target column', async () => {
    seed([
      ticket(1, { status: 'backlog', position: 1024 }),
      ticket(2, { status: 'backlog', position: 2048 }),
      ticket(3, { status: 'todo', position: 1024 }),
      ticket(4, { status: 'todo', position: 2048 }),
    ])
    const b = useBoardStore()
    await b.load('WEB')
    const d1 = deferred<unknown>()
    T.move.mockReturnValueOnce(d1.promise)
    T.move.mockResolvedValueOnce({ ...full(ticket(2, { status: 'todo', position: 3000 })), renumbered: false })
    const p1 = b.moveTicket('t3', 'done', { place: 'top' })
    // t3 left the todo column, so "after t3" is stale by the time this request is sent
    const p2 = b.moveTicket('t2', 'todo', { after: 't3' })
    d1.resolve({ ...full(ticket(3, { status: 'done', position: 1024 })), renumbered: false })
    await Promise.all([p1, p2])
    expect(T.move.mock.calls[1]).toEqual(['t2', { status: 'todo', after: 't4' }])
  })

  it('a 404 on the moved ticket removes it, drops the dependent move and refetches the project', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.move.mockReturnValueOnce(d1.promise)
    const p1 = b.moveTicket('t1', 'todo', { after: 't3' })
    const p2 = b.moveTicket('t2', 'todo', { after: 't1' })
    d1.reject(apiError(404, 'not_found'))
    expect(await Promise.all([p1, p2])).toEqual([false, false])
    expect(T.move).toHaveBeenCalledTimes(1)
    expect(b.ticketsById.t1).toBeUndefined()
    expect(b.columns.backlog).toEqual(['t2'])
  })

  it('renumbered triggers a full quiet reload', async () => {
    const b = await loaded()
    T.move.mockResolvedValue({ ...full(ticket(1, { status: 'todo', position: 1 })), renumbered: true })
    await b.moveTicket('t1', 'todo', { place: 'top' })
    await flush()
    expect(T.listAll).toHaveBeenCalledTimes(2)
  })

  it('a failing move drops queued followers and rolls them back', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.move.mockReturnValueOnce(d1.promise)
    const p1 = b.moveTicket('t1', 'todo', { after: 't3' })
    const p2 = b.moveTicket('t2', 'todo', { after: 't1' })
    d1.reject(apiError(500, 'internal', 'boom'))
    expect(await Promise.all([p1, p2])).toEqual([false, false])
    expect(b.columns.backlog).toEqual(['t1', 't2'])
    expect(b.columns.todo).toEqual(['t3'])
    expect(b.pendingMoves.size).toBe(0)
  })
})

describe('board.createTicket', () => {
  async function loaded() {
    seed([ticket(1)])
    const b = useBoardStore()
    await b.load('WEB')
    return b
  }
  it('shows a temp card then reconciles with the 201 (no duplicate when the event came first)', async () => {
    const b = await loaded()
    const d = deferred<unknown>()
    T.create.mockReturnValue(d.promise)
    const p = b.createTicket('backlog', 'New one')
    const tmpId = b.columns.backlog.find((id) => id.startsWith('tmp_'))!
    expect(tmpId).toBeTruthy()
    expect(b.ticketsById[tmpId].title).toBe('New one')
    expect(b.counts.backlog).toBe(1)
    const created = full(ticket(2, { title: 'New one', position: 9000 }))
    // ticket.created event arrives before the response
    T.get.mockResolvedValue(created)
    await b.applyEvent({ type: 'ticket.created', project_id: 'p1', ticket_id: 't2', actor: { type: 'user', id: ME.id }, at: '' })
    d.resolve(created)
    await p
    expect(b.columns.backlog).toEqual(['t1', 't2'])
    expect(Object.keys(b.ticketsById).filter((k) => k.startsWith('tmp_'))).toEqual([])
  })
  it('removes the temp card and rethrows on error (422 without a toast)', async () => {
    const b = await loaded()
    T.create.mockRejectedValue(apiError(422, 'validation_failed', 'title too long'))
    await expect(b.createTicket('todo', 'x')).rejects.toMatchObject({ status: 422 })
    expect(b.columns.todo).toEqual([])
    expect(notify).not.toHaveBeenCalled()
  })
  it('temp cards survive a reload', async () => {
    const b = await loaded()
    const d = deferred<unknown>()
    T.create.mockReturnValue(d.promise)
    void b.createTicket('backlog', 'pending')
    await b.load('WEB', { quiet: true })
    expect(b.columns.backlog.some((id) => id.startsWith('tmp_'))).toBe(true)
  })
})

describe('board.updateTicket / deleteTicket', () => {
  it('applies optimistically and rolls back on failure', async () => {
    seed([ticket(1)])
    const b = useBoardStore()
    await b.load('WEB')
    T.update.mockRejectedValue(apiError(422, 'validation_failed', 'bad'))
    await expect(b.updateTicket('t1', { title: 'New', priority: 'urgent' })).rejects.toBeTruthy()
    expect(b.ticketsById.t1.title).toBe('Ticket 1')
    expect(b.ticketsById.t1.priority).toBe('medium')
  })
  it('resolves assignee and labels from members and labels, then adopts the response', async () => {
    seed([ticket(1)])
    const b = useBoardStore()
    await b.load('WEB')
    const d = deferred<unknown>()
    T.update.mockReturnValue(d.promise)
    const p = b.updateTicket('t1', { assignee: 'u2', labels: ['l1'] })
    expect(b.ticketsById.t1.assignee?.display_name).toBe('Bob')
    expect(b.ticketsById.t1.labels.map((l) => l.id)).toEqual(['l1'])
    d.resolve(full(ticket(1, { title: 'Server' }), 'desc'))
    await p
    expect(b.ticketsById.t1.title).toBe('Server')
    expect(b.fullById.t1.description).toBe('desc')
  })
  it('deleteTicket removes the card after the server accepts', async () => {
    seed([ticket(1), ticket(2)])
    const b = useBoardStore()
    await b.load('WEB')
    T.remove.mockResolvedValue(undefined)
    await b.deleteTicket('t1')
    expect(b.columns.backlog).toEqual(['t2'])
  })
})
