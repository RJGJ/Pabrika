import { afterEach, describe, expect, it, vi } from 'vitest'
import { activityFeed } from '@/api/activity'
import { decisions } from '@/api/decisions'
import { EVENT_TYPES } from '@/api/types'

function stub(body: unknown = {}, status = 200) {
  const fn = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify(body), { status }))
  vi.stubGlobal('fetch', fn)
  return fn
}
afterEach(() => vi.unstubAllGlobals())

describe('decisions api', () => {
  it('POSTs the decision to the ticket', async () => {
    const fn = stub({ id: 'd1' }, 201)
    await decisions.create('t 1', { gate: 'plan', verdict: 'changes_requested', note: 'no' })
    expect(fn.mock.calls[0][0]).toBe('/api/v1/tickets/t%201/decisions')
    expect(fn.mock.calls[0][1]?.method).toBe('POST')
    expect(JSON.parse(String(fn.mock.calls[0][1]?.body))).toEqual({ gate: 'plan', verdict: 'changes_requested', note: 'no' })
  })
  it('lists a page with a cursor', async () => {
    const fn = stub({ items: [], next_cursor: null })
    await decisions.list('t1', 'abc')
    expect(fn.mock.calls[0][0]).toBe('/api/v1/tickets/t1/decisions?limit=50&cursor=abc')
  })
})

describe('activity feed api', () => {
  it('builds the query and drops empty params', async () => {
    const fn = stub({ items: [], next_cursor: null })
    await activityFeed.list('WEB', { cursor: 'c1', actor_type: 'api_token' })
    expect(fn.mock.calls[0][0]).toBe('/api/v1/projects/WEB/activity?limit=50&cursor=c1&actor_type=api_token')
  })
})

describe('events', () => {
  it('knows decision.recorded', () => {
    expect(EVENT_TYPES).toContain('decision.recorded')
  })
})
