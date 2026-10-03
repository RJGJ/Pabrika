import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { effectScope, nextTick, ref, type EffectScope } from 'vue'

vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn() } }))
vi.mock('@/api/labels', () => ({ labels: { list: vi.fn() } }))
vi.mock('@/api/tickets', () => ({ tickets: { listAll: vi.fn(), get: vi.fn(), move: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { members as membersApi } from '@/api/members'
import { labels as labelsApi } from '@/api/labels'
import { projects as projectsApi } from '@/api/projects'
import { tickets as ticketsApi } from '@/api/tickets'
import { setEventSourceFactory, type EventSourceLike } from '@/api/events'
import { useProjectEvents } from '@/composables/useProjectEvents'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { apiError, flush, full, label, member, ME, project, ticket, type Mocked } from '../stores/helpers'

const P = projectsApi as unknown as Mocked
const M = membersApi as unknown as Mocked
const L = labelsApi as unknown as Mocked
const T = ticketsApi as unknown as Mocked

class FakeEventSource implements EventSourceLike {
  static all: FakeEventSource[] = []
  readyState = 0
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  closed = false
  listeners = new Map<string, ((ev: MessageEvent) => void)[]>()
  constructor(public url: string) {
    FakeEventSource.all.push(this)
  }
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l])
  }
  close() {
    this.closed = true
    this.readyState = 2
  }
  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }
  fail(readyState: 0 | 2) {
    this.readyState = readyState
    this.onerror?.(new Event('error'))
  }
  emit(type: string, data: unknown) {
    for (const l of this.listeners.get(type) ?? []) l({ data: JSON.stringify(data) } as MessageEvent)
  }
}
const last = () => FakeEventSource.all[FakeEventSource.all.length - 1]

let scope: EffectScope
const key = ref<string | null>('WEB')

async function setup() {
  P.get.mockResolvedValue(project())
  M.list.mockResolvedValue([member('u-me')])
  L.list.mockResolvedValue([label('l1')])
  T.listAll.mockResolvedValue([ticket(1)])
  const board = useBoardStore()
  await board.load('WEB')
  scope = effectScope()
  scope.run(() => useProjectEvents(() => key.value))
  await nextTick()
  return board
}

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
  for (const m of [P, M, L, T]) for (const f of Object.values(m)) f.mockReset()
  FakeEventSource.all = []
  key.value = 'WEB'
  setEventSourceFactory((url) => new FakeEventSource(url))
  useAuthStore().user = ME
})
afterEach(() => {
  scope?.stop()
  vi.useRealTimers()
})

describe('useProjectEvents', () => {
  it('opens the project stream once the board is ready', async () => {
    await setup()
    expect(FakeEventSource.all).toHaveLength(1)
    expect(last().url).toBe('/api/v1/projects/WEB/events')
  })

  it('does not open before the board is ready', async () => {
    const board = useBoardStore()
    scope = effectScope()
    scope.run(() => useProjectEvents(() => key.value))
    await nextTick()
    expect(FakeEventSource.all).toHaveLength(0)
    expect(board.live).toBe('idle')
  })

  it('every open, including the first, triggers one quiet reload and sets Live', async () => {
    const board = await setup()
    T.listAll.mockClear()
    last().open()
    await flush()
    expect(board.live).toBe('live')
    expect(T.listAll).toHaveBeenCalledTimes(1)
    expect(board.loadState).toBe('ready') // quiet: no skeleton
    last().open() // browser-level reconnect reuses the same EventSource
    await flush()
    expect(T.listAll).toHaveBeenCalledTimes(2)
  })

  it('CONNECTING error shows Reconnecting and does nothing else (no probe)', async () => {
    const board = await setup()
    last().open()
    await flush()
    P.get.mockClear()
    last().fail(0)
    expect(board.live).toBe('reconnecting')
    expect(P.get).not.toHaveBeenCalled()
    expect(FakeEventSource.all).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeEventSource.all).toHaveLength(1)
  })

  it('CLOSED error probes the project; a 200 recreates the stream with capped backoff', async () => {
    const board = await setup()
    const first = last()
    first.fail(2)
    expect(first.closed).toBe(true)
    await flush()
    expect(board.live).toBe('reconnecting')
    expect(P.get).toHaveBeenCalledTimes(2) // load + probe
    expect(FakeEventSource.all).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(2999)
    expect(FakeEventSource.all).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.all).toHaveLength(2)
    // fails again without ever opening: 6 s, then 12 s
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(5999)
    expect(FakeEventSource.all).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.all).toHaveLength(3)
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(12000)
    expect(FakeEventSource.all).toHaveLength(4)
    // a successful open resets the backoff
    last().open()
    await flush()
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(3000)
    expect(FakeEventSource.all).toHaveLength(5)
  })

  it('backoff is capped at 30 s', async () => {
    await setup()
    for (let i = 0; i < 6; i++) {
      last().fail(2)
      await flush()
      await vi.advanceTimersByTimeAsync(30000)
    }
    const n = FakeEventSource.all.length
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(29999)
    expect(FakeEventSource.all).toHaveLength(n)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.all).toHaveLength(n + 1)
  })

  it('a 503 probe recreates the stream; a 429 probe waits for Retry-After', async () => {
    await setup()
    P.get.mockRejectedValue(Object.assign(apiError(503, 'unavailable', 'x')))
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(3000)
    expect(FakeEventSource.all).toHaveLength(2)
    const e429 = apiError(429, 'rate_limited', 'x')
    e429.retryAfter = 20
    P.get.mockRejectedValue(e429)
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(19999)
    expect(FakeEventSource.all).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.all).toHaveLength(3)
  })

  it('a 404 probe runs the lost-access path and does not reconnect', async () => {
    const board = await setup()
    P.get.mockRejectedValue(apiError(404, 'not_found', 'x'))
    last().fail(2)
    await flush()
    expect(board.lostAccess).toBe(true)
    expect(board.loadState).toBe('no-access')
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeEventSource.all).toHaveLength(1)
  })

  it('board.leaving suppresses the lost-access dialog', async () => {
    const board = await setup()
    board.leaving = true
    P.get.mockRejectedValue(apiError(404, 'not_found', 'x'))
    last().fail(2)
    await flush()
    expect(board.lostAccess).toBe(false)
    expect(board.loadState).toBe('ready')
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeEventSource.all).toHaveLength(1)
  })

  it('a 401 probe does not reconnect (the client handles the login redirect)', async () => {
    await setup()
    P.get.mockRejectedValue(apiError(401, 'unauthorized', 'x'))
    last().fail(2)
    await flush()
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeEventSource.all).toHaveLength(1)
  })

  it('routes named events into the board store', async () => {
    const board = await setup()
    T.get.mockResolvedValue(full(ticket(1, { title: 'Live edit' })))
    last().emit('ticket.updated', { project_id: 'p1', ticket_id: 't1', actor: { type: 'api_token', id: 'tok' }, at: '' })
    await flush()
    expect(board.ticketsById.t1.title).toBe('Live edit')
    expect(board.flashIds.has('t1')).toBe(true)
  })

  it('closes on key change, on reset (logout or 401) and on scope disposal', async () => {
    const board = await setup()
    const first = last()
    key.value = 'OTHER'
    board.reset() // the board store follows the route
    await nextTick()
    expect(first.closed).toBe(true)

    // reopen after a fresh load
    board.reset()
    await board.load('OTHER')
    await nextTick()
    const second = last()
    expect(second.url).toContain('OTHER')
    board.reset()
    await nextTick()
    expect(second.closed).toBe(true)
    expect(board.live).toBe('idle')

    await board.load('OTHER')
    await nextTick()
    const third = last()
    scope.stop()
    expect(third.closed).toBe(true)
  })

  it('does not reopen when only the key case changes (canonical redirect)', async () => {
    await setup()
    key.value = 'web'
    await nextTick()
    expect(FakeEventSource.all).toHaveLength(1)
  })
})
