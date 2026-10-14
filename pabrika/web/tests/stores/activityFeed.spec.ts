import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/api/activity', () => ({ activityFeed: { list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn() } }))

import { activityFeed } from '@/api/activity'
import { members as membersApi } from '@/api/members'
import type { ActivityFeedItem } from '@/api/types'
import { useActivityFeedStore } from '@/stores/activityFeed'
import { apiError, event, flush, member, type Mocked } from './helpers'

const F = activityFeed as unknown as Mocked
const M = membersApi as unknown as Mocked

const row = (n: number, over: Partial<ActivityFeedItem> = {}): ActivityFeedItem => ({
  id: `i${n}`, kind: 'activity', ticket: { id: 't1', ref: 'WEB-1', title: 'T' },
  actor: { type: 'user', id: 'u1', name: 'Ana', bot: false }, action: 'updated', changes: {},
  created_at: `2026-10-0${n}T00:00:00Z`, ...over,
})

beforeEach(() => {
  setActivePinia(createPinia())
  F.list.mockReset()
  M.list.mockReset()
  M.list.mockResolvedValue([member('u1')])
})

describe('activity feed store', () => {
  it('loads the first page and the members', async () => {
    F.list.mockResolvedValue({ items: [row(3), row(2)], next_cursor: 'c1' })
    const s = useActivityFeedStore()
    await s.load('WEB')
    expect(s.loadState).toBe('ready')
    expect(s.items.map((i) => i.id)).toEqual(['i3', 'i2'])
    expect(s.hasMore).toBe(true)
    expect(F.list.mock.calls[0][0]).toBe('WEB')
    expect(F.list.mock.calls[0][1]).toMatchObject({ cursor: null })
  })

  it('load more appends without duplicates', async () => {
    F.list.mockResolvedValueOnce({ items: [row(3), row(2)], next_cursor: 'c1' })
    const s = useActivityFeedStore()
    await s.load('WEB')
    F.list.mockResolvedValueOnce({ items: [row(2), row(1)], next_cursor: null })
    await s.loadMore()
    expect(s.items.map((i) => i.id)).toEqual(['i3', 'i2', 'i1'])
    expect(F.list.mock.calls[1][1]).toMatchObject({ cursor: 'c1' })
    expect(s.hasMore).toBe(false)
  })

  it('404 is not-found and other failures are an error', async () => {
    F.list.mockRejectedValueOnce(apiError(404, 'not_found'))
    const s = useActivityFeedStore()
    await s.load('NOPE')
    expect(s.loadState).toBe('not-found')
    F.list.mockRejectedValueOnce(apiError(500, 'internal'))
    await s.load('WEB')
    expect(s.loadState).toBe('error')
  })

  it('counts relevant events as pending without touching the list, then showNew reloads from the top', async () => {
    F.list.mockResolvedValue({ items: [row(2)], next_cursor: null })
    const s = useActivityFeedStore()
    await s.load('WEB')
    s.noteEvent(event('ticket.updated', { ticket_id: 't1' }))
    s.noteEvent(event('comment.added', { ticket_id: 't1' }))
    s.noteEvent(event('decision.recorded', { ticket_id: 't1' }))
    s.noteEvent(event('label.changed'))
    s.noteEvent(event('member.changed'))
    expect(s.pending).toBe(3)
    expect(s.items.map((i) => i.id)).toEqual(['i2'])
    F.list.mockResolvedValue({ items: [row(4), row(3), row(2)], next_cursor: null })
    await s.showNew()
    expect(s.pending).toBe(0)
    expect(s.items.map((i) => i.id)).toEqual(['i4', 'i3', 'i2'])
  })

  it('actor filter goes to the server; other filters stay client side', async () => {
    F.list.mockResolvedValue({ items: [row(2)], next_cursor: null })
    const s = useActivityFeedStore()
    await s.load('WEB')
    await s.setFilters({ actor: 'agent', role: null, ticket: '', gates: false })
    expect(F.list.mock.calls[1][1]).toMatchObject({ actor_type: 'api_token' })
    await s.setFilters({ actor: 'agent', role: null, ticket: 'WEB-9', gates: false })
    expect(F.list).toHaveBeenCalledTimes(2) // ticket is client side only
    expect(s.visible).toEqual([])
  })

  it('a reconnect checks the top row and reports new events', async () => {
    F.list.mockResolvedValue({ items: [row(2)], next_cursor: null })
    const s = useActivityFeedStore()
    await s.load('WEB')
    await s.onStreamOpen() // first open: nothing to compare
    expect(F.list).toHaveBeenCalledTimes(1)
    F.list.mockResolvedValue({ items: [row(3), row(2)], next_cursor: null })
    await s.onStreamOpen()
    await flush()
    expect(s.pending).toBe(1)
  })

  it('keeps the loaded list when the same project is loaded again', async () => {
    F.list.mockResolvedValue({ items: [row(3), row(2)], next_cursor: 'c1' })
    const s = useActivityFeedStore()
    await s.load('WEB')
    F.list.mockResolvedValue({ items: [row(3), row(2)], next_cursor: 'c1' })
    const p = s.load('web')
    expect(s.loadState).toBe('ready')
    expect(s.items).toHaveLength(2)
    await p
  })
})
