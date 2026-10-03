import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

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
import { setRouter } from '@/router/instance'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'
import { apiError, deferred, event, flush, full, label, member, ME, project, ticket, type Mocked } from './helpers'

const P = projectsApi as unknown as Mocked
const M = membersApi as unknown as Mocked
const L = labelsApi as unknown as Mocked
const T = ticketsApi as unknown as Mocked

async function loaded(list = [ticket(1), ticket(2), ticket(3, { status: 'todo' })]) {
  P.get.mockResolvedValue(project())
  M.list.mockResolvedValue([member('u-me')])
  L.list.mockResolvedValue([label('l1')])
  T.listAll.mockResolvedValue(list)
  const b = useBoardStore()
  await b.load('WEB')
  vi.clearAllMocks()
  P.get.mockResolvedValue(project())
  M.list.mockResolvedValue([member('u-me')])
  L.list.mockResolvedValue([label('l1')])
  T.listAll.mockResolvedValue(list)
  return b
}

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
  for (const m of [P, M, L, T]) for (const f of Object.values(m)) f.mockReset()
  ;(notify as unknown as ReturnType<typeof vi.fn>).mockReset()
  useAuthStore().user = ME
})
afterEach(() => vi.useRealTimers())

describe('applyEvent: tickets', () => {
  it('ticket.updated fetches only that ticket (one GET, no list call) and flashes for others', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(1, { title: 'Changed', priority: 'urgent' })))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1' }))
    expect(T.get).toHaveBeenCalledTimes(1)
    expect(T.get.mock.calls[0][0]).toBe('t1')
    expect(T.listAll).not.toHaveBeenCalled()
    expect(b.ticketsById.t1.title).toBe('Changed')
    expect(b.flashIds.has('t1')).toBe(true)
    vi.advanceTimersByTime(1600)
    expect(b.flashIds.has('t1')).toBe(false)
  })

  it('does not flash for my own events but still refreshes', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(1, { title: 'Mine' })))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1', actor: { type: 'user', id: ME.id } }))
    expect(b.ticketsById.t1.title).toBe('Mine')
    expect(b.flashIds.size).toBe(0)
  })

  it('an api_token actor always flashes, even with my user id', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(1)))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1', actor: { type: 'api_token', id: ME.id } }))
    expect(b.flashIds.has('t1')).toBe(true)
  })

  it('ticket.moved re-slots into the new column by position', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(1, { status: 'todo', position: 500 })))
    await b.applyEvent(event('ticket.moved', { ticket_id: 't1' }))
    expect(b.columns.backlog).toEqual(['t2'])
    expect(b.columns.todo).toEqual(['t1', 't3'])
  })

  it('ticket.moved with renumbered triggers a full reload, no single GET', async () => {
    const b = await loaded()
    await b.applyEvent(event('ticket.moved', { ticket_id: 't1', renumbered: true }))
    expect(T.listAll).toHaveBeenCalledTimes(1)
    expect(P.get).toHaveBeenCalledTimes(1)
    expect(T.get).not.toHaveBeenCalled()
  })

  it('ticket.created adds the card', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(9, { position: 99999 })))
    await b.applyEvent(event('ticket.created', { ticket_id: 't9' }))
    expect(b.columns.backlog).toEqual(['t1', 't2', 't9'])
  })

  it('ticket.deleted removes the card without a fetch; closes the panel and toasts only when not mine', async () => {
    const b = await loaded()
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
        { path: '/p/:key/t/:number', name: 'ticket', component: { template: '<i/>' } },
      ],
    })
    setRouter(router)
    await router.push('/p/WEB/t/1?q=x')
    b.selectedRef = 'WEB-1'
    await b.applyEvent(event('ticket.deleted', { ticket_id: 't1' }))
    expect(T.get).not.toHaveBeenCalled()
    expect(b.columns.backlog).toEqual(['t2'])
    expect(notify).toHaveBeenCalledWith('info', 'WEB-1 was deleted')
    await vi.advanceTimersByTimeAsync(0)
    expect(router.currentRoute.value.name).toBe('board')
    expect(router.currentRoute.value.query).toEqual({ q: 'x' })

    ;(notify as unknown as ReturnType<typeof vi.fn>).mockReset()
    b.selectedRef = 'WEB-2'
    await b.applyEvent(event('ticket.deleted', { ticket_id: 't2', actor: { type: 'user', id: ME.id } }))
    expect(notify).not.toHaveBeenCalled()
    expect(b.flashIds.size).toBe(0)
  })

  it('a 404 on refreshTicket removes the card and is not lost access', async () => {
    const b = await loaded()
    T.get.mockRejectedValue(apiError(404, 'not_found'))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1' }))
    expect(b.ticketsById.t1).toBeUndefined()
    expect(b.lostAccess).toBe(false)
    expect(b.loadState).toBe('ready')
    expect(b.flashIds.size).toBe(0)
  })

  it('ignores events for another project', async () => {
    const b = await loaded()
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1', project_id: 'other' }))
    expect(T.get).not.toHaveBeenCalled()
  })
})

describe('refreshTicket', () => {
  it('coalesces rapid events: one in-flight fetch plus one trailing refetch', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.get.mockReturnValueOnce(d1.promise)
    T.get.mockResolvedValueOnce(full(ticket(1, { title: 'Second' })))
    const a = b.refreshTicket('t1')
    const c = b.refreshTicket('t1')
    const d = b.refreshTicket('t1')
    expect(T.get).toHaveBeenCalledTimes(1)
    d1.resolve(full(ticket(1, { title: 'First' })))
    await Promise.all([a, c, d])
    expect(T.get).toHaveBeenCalledTimes(2)
  })

  it('wait: an event arriving mid-fetch refetches once after it', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.get.mockReturnValueOnce(d1.promise)
    T.get.mockResolvedValueOnce(full(ticket(1, { title: 'Latest' })))
    const a = b.refreshTicket('t1')
    void b.refreshTicket('t1')
    d1.resolve(full(ticket(1, { title: 'Stale' })))
    await a
    expect(b.ticketsById.t1.title).toBe('Latest')
  })

  it('discards a result older than a local mutation and refetches', async () => {
    const b = await loaded()
    const d1 = deferred<unknown>()
    T.get.mockReturnValueOnce(d1.promise)
    T.get.mockResolvedValueOnce(full(ticket(1, { title: 'Saved title' })))
    const r = b.refreshTicket('t1')
    T.update.mockResolvedValue(full(ticket(1, { title: 'Saved title' })))
    await b.updateTicket('t1', { title: 'Saved title' })
    d1.resolve(full(ticket(1, { title: 'Old server copy' })))
    await r
    expect(b.ticketsById.t1.title).toBe('Saved title')
    expect(T.get).toHaveBeenCalledTimes(2)
  })

  it('holds the refresh for a ticket with a pending move until it settles', async () => {
    const b = await loaded()
    const mv = deferred<unknown>()
    T.move.mockReturnValueOnce(mv.promise)
    T.get.mockResolvedValue(full(ticket(1, { status: 'backlog', position: 1024 })))
    const moved = b.moveTicket('t1', 'todo', { place: 'bottom' })
    await b.refreshTicket('t1')
    expect(b.ticketsById.t1.status).toBe('todo') // the stale server copy was not applied
    mv.resolve({ ...full(ticket(1, { status: 'todo', position: 4096 })), renumbered: false })
    await moved
    await flush()
    expect(T.get).toHaveBeenCalledTimes(2) // refetched after the move settled
  })
})

describe('applyEvent: other events', () => {
  it('comment events bump the comment version and refresh the ticket (comment count)', async () => {
    const b = await loaded()
    T.get.mockResolvedValue(full(ticket(1, { comment_count: 2 })))
    await b.applyEvent(event('comment.added', { ticket_id: 't1', comment_id: 'c1' }))
    expect(b.commentVersions.t1).toBe(1)
    expect(b.ticketsById.t1.comment_count).toBe(2)
    await b.applyEvent(event('comment.changed', { ticket_id: 't1', comment_id: 'c1' }))
    expect(b.commentVersions.t1).toBe(2)
  })

  it('label.changed refetches labels and the ticket list (not the project)', async () => {
    const b = await loaded()
    await b.applyEvent(event('label.changed', { label_id: 'l1' }))
    expect(L.list).toHaveBeenCalledTimes(1)
    expect(T.listAll).toHaveBeenCalledTimes(1)
    expect(P.get).not.toHaveBeenCalled()
  })

  it('member.changed refetches members, tickets and the project role', async () => {
    const b = await loaded()
    P.get.mockResolvedValue(project({ role: 'viewer' }))
    await b.applyEvent(event('member.changed', { user_id: ME.id }))
    expect(M.list).toHaveBeenCalledTimes(1)
    expect(T.listAll).toHaveBeenCalledTimes(1)
    expect(P.get).toHaveBeenCalledTimes(1)
    expect(b.isViewer).toBe(true)
  })

  it('member.changed whose project call is 404 is lost access', async () => {
    const b = await loaded()
    P.get.mockRejectedValue(apiError(404, 'not_found'))
    await b.applyEvent(event('member.changed', { user_id: ME.id }))
    expect(b.lostAccess).toBe(true)
  })

  it('project.updated refetches the project and the sidebar list', async () => {
    const b = await loaded()
    const projects = useProjectsStore()
    const fetchSpy = vi.spyOn(projects, 'fetch').mockResolvedValue()
    P.get.mockResolvedValue(project({ archived_at: '2026-01-01T00:00:00Z' }))
    await b.applyEvent(event('project.updated'))
    expect(P.get).toHaveBeenCalledTimes(1)
    expect(fetchSpy).toHaveBeenCalled()
    expect(b.isArchived).toBe(true)
    expect(b.canEdit).toBe(false)
    expect(T.listAll).not.toHaveBeenCalled()
  })

  it('ignores ticket events when tickets are not loaded (settings view)', async () => {
    P.get.mockResolvedValue(project())
    M.list.mockResolvedValue([])
    L.list.mockResolvedValue([])
    const b = useBoardStore()
    await b.load('WEB', { tickets: false })
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1' }))
    expect(T.get).not.toHaveBeenCalled()
  })
})

describe('drag deferral', () => {
  it('defers events for the dragged column and applies them in order after the drop', async () => {
    const b = await loaded()
    b.startDrag('backlog')
    T.get.mockImplementation(async (id: string) => full(ticket(Number(id.slice(1)), { title: `new ${id}` })))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't1' }))
    await b.applyEvent(event('ticket.updated', { ticket_id: 't2' }))
    expect(T.get).not.toHaveBeenCalled()
    expect(b.deferredEvents.map((e) => e.ticket_id)).toEqual(['t1', 't2'])
    // another column applies immediately
    await b.applyEvent(event('ticket.updated', { ticket_id: 't3' }))
    expect(T.get).toHaveBeenCalledTimes(1)
    b.endDrag()
    await flush()
    expect(T.get.mock.calls.map((c) => c[0])).toEqual(['t3', 't1', 't2'])
    expect(b.deferredEvents).toEqual([])
    expect(b.ticketsById.t1.title).toBe('new t1')
  })

  it('also holds events for a ticket with a pending move until the queue settles', async () => {
    const b = await loaded()
    const mv = deferred<unknown>()
    T.move.mockReturnValueOnce(mv.promise)
    const moved = b.moveTicket('t1', 'done', { place: 'top' })
    T.get.mockResolvedValue(full(ticket(1, { status: 'done', position: 10 })))
    await b.applyEvent(event('ticket.moved', { ticket_id: 't1' }))
    expect(T.get).not.toHaveBeenCalled()
    mv.resolve({ ...full(ticket(1, { status: 'done', position: 10 })), renumbered: false })
    await moved
    await flush()
    expect(T.get).toHaveBeenCalledTimes(1)
  })
})
