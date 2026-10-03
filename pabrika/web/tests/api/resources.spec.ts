import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { configureClient, resetUnauthorizedLatch } from '@/api/client'
import { tickets } from '@/api/tickets'
import { tokens } from '@/api/tokens'
import { auth } from '@/api/auth'
import { projects } from '@/api/projects'
import { openProjectStream, setEventSourceFactory, type EventSourceLike } from '@/api/events'
import { EVENT_TYPES } from '@/api/types'

function stub(body: unknown = {}, status = 200) {
  const fn = vi.fn(async (_url: string, _init?: RequestInit) =>
    status === 204 ? new Response(null, { status }) : new Response(JSON.stringify(body), { status }),
  )
  vi.stubGlobal('fetch', fn)
  return fn
}
const bodyOf = (fn: ReturnType<typeof stub>) => JSON.parse(String(fn.mock.calls[0][1]?.body))

const onUnauthorized = vi.fn()
beforeEach(() => {
  onUnauthorized.mockReset()
  resetUnauthorizedLatch()
  configureClient({ onUnauthorized, onToast: () => {}, onForbidden: () => {}, sleep: async () => {} })
})
afterEach(() => vi.unstubAllGlobals())

describe('resource modules', () => {
  it('tickets.move sends status plus a single placement', async () => {
    const fn = stub({})
    await tickets.move('01T', { status: 'todo', after: '01A' })
    expect(fn.mock.calls[0][0]).toBe('/api/v1/tickets/01T/move')
    expect(fn.mock.calls[0][1]?.method).toBe('POST')
    expect(bodyOf(fn)).toEqual({ status: 'todo', after: '01A' })
  })
  it('tickets.get accepts a reference', async () => {
    const fn = stub({})
    await tickets.get('WEB-12')
    expect(fn.mock.calls[0][0]).toBe('/api/v1/tickets/WEB-12')
  })
  it('tokens.create sends the project ULID as project_id', async () => {
    const fn = stub({})
    await tokens.create({ name: 'agent', scope: 'write', project_id: '01PROJ' })
    expect(bodyOf(fn)).toEqual({ name: 'agent', scope: 'write', project_id: '01PROJ' })
  })
  it('projects.list reads the items and sends archived only when asked', async () => {
    const fn = stub({ items: [{ key: 'WEB' }], next_cursor: null })
    expect(await projects.list()).toEqual([{ key: 'WEB' }])
    expect(fn.mock.calls[0][0]).toBe('/api/v1/projects')
    await projects.list(true)
    expect(fn.mock.calls[1][0]).toBe('/api/v1/projects?archived=true')
  })
  it('POST without payload still sends {}', async () => {
    const fn = stub({}, 200)
    await auth.login('a@b.c', 'pw')
    expect(bodyOf(fn)).toEqual({ email: 'a@b.c', password: 'pw' })
  })
  it('logout sends no body', async () => {
    const fn = stub({}, 204)
    await auth.logout()
    expect(fn.mock.calls[0][1]?.body).toBeUndefined()
  })
  it('auth.me 401 does not trigger the global handler', async () => {
    stub({ error: { code: 'unauthorized', message: 'no' } }, 401)
    await auth.me().catch(() => {})
    expect(onUnauthorized).not.toHaveBeenCalled()
  })
})

class Fake implements EventSourceLike {
  readyState = 0
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  listeners: Record<string, (ev: MessageEvent) => void> = {}
  closed = false
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners[type] = l
  }
  close() {
    this.closed = true
  }
}

describe('openProjectStream', () => {
  it('registers one listener per event type and forwards parsed events', () => {
    const fake = new Fake()
    setEventSourceFactory(() => fake)
    const onEvent = vi.fn()
    const onOpen = vi.fn()
    const onError = vi.fn()
    const s = openProjectStream('WEB', { onEvent, onOpen, onError })
    expect(Object.keys(fake.listeners).sort()).toEqual([...EVENT_TYPES].sort())
    fake.listeners['ticket.updated'](new MessageEvent('ticket.updated', { data: '{"project_id":"p","ticket_id":"t","actor":{"type":"user","id":"u"},"at":"x"}' }))
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ type: 'ticket.updated', ticket_id: 't' }))
    fake.listeners['ticket.updated'](new MessageEvent('ticket.updated', { data: 'not json' }))
    expect(onEvent).toHaveBeenCalledTimes(1)
    fake.onopen?.(new Event('open'))
    expect(onOpen).toHaveBeenCalled()
    fake.readyState = 2
    fake.onerror?.(new Event('error'))
    expect(onError).toHaveBeenCalledWith(2)
    s.close()
    expect(fake.closed).toBe(true)
  })
})
