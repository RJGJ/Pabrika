import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

vi.mock('@/api/activity', () => ({ activityFeed: { list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn() } }))
vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), list: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { activityFeed } from '@/api/activity'
import { setEventSourceFactory, type EventSourceLike } from '@/api/events'
import { members as membersApi } from '@/api/members'
import type { ActivityFeedItem } from '@/api/types'
import ProjectActivityView from '@/views/ProjectActivityView.vue'
import { useActivityFeedStore } from '@/stores/activityFeed'
import { apiError, member, type Mocked } from '../../stores/helpers'

const F = activityFeed as unknown as Mocked
const M = membersApi as unknown as Mocked

class FakeES implements EventSourceLike {
  static all: FakeES[] = []
  readyState = 0
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  listeners = new Map<string, ((ev: MessageEvent) => void)[]>()
  constructor(public url: string) {
    FakeES.all.push(this)
  }
  addEventListener(t: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), l])
  }
  close() {
    this.readyState = 2
  }
  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }
  emit(type: string, data: object) {
    for (const l of this.listeners.get(type) ?? []) l(new MessageEvent(type, { data: JSON.stringify(data) }))
  }
}

const human = { type: 'user' as const, id: 'u1', name: 'Ana', bot: false }
const agent = { type: 'api_token' as const, id: 'k1', name: 'developer-bot', bot: true }
const row = (n: number, over: Partial<ActivityFeedItem> = {}): ActivityFeedItem => ({
  id: `i${n}`, kind: 'activity', ticket: { id: 't1', ref: 'WEB-30', title: 'CSV export' }, actor: human,
  action: 'moved', changes: { status: ['todo', 'in_progress'] }, created_at: `2026-10-0${n}T00:00:00Z`, ...over,
})

let wrapper: VueWrapper | null = null
async function open(url = '/p/WEB/activity') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key/activity', name: 'project-activity', component: ProjectActivityView },
      { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      { path: '/p/:key/t/:number', name: 'ticket', component: { template: '<i/>' } },
      { path: '/', name: 'home', component: { template: '<i/>' } },
    ],
  })
  await router.push(url)
  wrapper = mount({ template: '<RouterView />' }, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return router
}

beforeEach(() => {
  setActivePinia(createPinia())
  FakeES.all = []
  setEventSourceFactory((url) => new FakeES(url))
  F.list.mockReset()
  M.list.mockReset()
  M.list.mockResolvedValue([member('u1', 'Ana')])
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('ProjectActivityView', () => {
  it('renders a row per kind with a role chip on agent comments', async () => {
    F.list.mockResolvedValue({
      items: [
        row(4, { id: 'c', kind: 'comment', actor: agent, action: 'commented', summary: '[reviewer] CHANGES REQUESTED\nfix the thing' }),
        row(3, { id: 'd', kind: 'decision', action: 'decision', changes: { plan: [null, 'approved'] } }),
        row(2, { id: 'a' }),
      ],
      next_cursor: null,
    })
    await open()
    const rows = [...document.body.querySelectorAll('[data-feed-id]')]
    expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual(['comment', 'decision', 'activity'])
    expect(rows[0].textContent).toContain('bot')
    expect(rows[0].querySelector('[data-testid=role-chip]')?.textContent).toBe('Reviewer')
    expect(rows[0].querySelector('[data-testid=type-chip]')?.textContent).toBe('Changes requested')
    expect(rows[0].textContent).toContain('fix the thing')
    expect(rows[1].textContent).toContain('approved the plan')
    expect(rows[2].textContent).toContain('moved from To do to In progress')
    expect(rows[2].querySelector('[data-testid=role-chip]')).toBeNull()
    expect(rows[2].querySelector('a')?.getAttribute('href')).toBe('/p/WEB/t/30')
  })

  it('shows the empty state', async () => {
    F.list.mockResolvedValue({ items: [], next_cursor: null })
    await open()
    expect(document.body.textContent).toContain('Nothing has happened in this project yet')
  })

  it('shows not found for a project the user cannot see', async () => {
    F.list.mockRejectedValue(apiError(404, 'not_found'))
    await open('/p/NOPE/activity')
    expect(document.body.textContent).toContain('Project not found')
  })

  it('restores filters from the URL and writes changes back', async () => {
    F.list.mockResolvedValue({
      items: [
        row(4, { id: 'c', kind: 'comment', actor: agent, action: 'commented', summary: '[qa] PASS' }),
        row(3, { id: 'a' }),
      ],
      next_cursor: null,
    })
    const router = await open('/p/WEB/activity?actor=agent&role=qa')
    expect(F.list.mock.calls[0][1]).toMatchObject({ actor_type: 'api_token' })
    expect([...document.body.querySelectorAll('[data-feed-id]')].map((r) => r.getAttribute('data-feed-id'))).toEqual(['c'])
    const gates = document.body.querySelector('[data-testid=gates-only]') as HTMLButtonElement
    gates.click()
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ actor: 'agent', role: 'qa', gates: '1' })
    expect(useActivityFeedStore().filters.gates).toBe(true)
    expect(document.body.querySelector('[data-testid=no-match]')).toBeTruthy()
  })

  it('load more appends without duplicates', async () => {
    F.list.mockResolvedValueOnce({ items: [row(4, { id: 'x4' }), row(3, { id: 'x3' })], next_cursor: 'c1' })
    await open()
    F.list.mockResolvedValueOnce({ items: [row(3, { id: 'x3' }), row(2, { id: 'x2' })], next_cursor: null })
    const more = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Load more')!
    more.click()
    await flushPromises()
    expect([...document.body.querySelectorAll('[data-feed-id]')].map((r) => r.getAttribute('data-feed-id'))).toEqual(['x4', 'x3', 'x2'])
    expect([...document.body.querySelectorAll('button')].some((b) => b.textContent?.trim() === 'Load more')).toBe(false)
  })

  it('live events show an "N new events" bar without moving the list; clicking it shows the new row', async () => {
    F.list.mockResolvedValue({ items: [row(2, { id: 'old' })], next_cursor: null })
    await open()
    const es = FakeES.all[0]
    es.open()
    await flushPromises()
    expect(document.body.querySelector('[data-testid=new-events]')).toBeNull()
    es.emit('ticket.updated', { project_id: 'p1', ticket_id: 't1', actor: { type: 'user', id: 'x' }, at: '' })
    es.emit('comment.added', { project_id: 'p1', ticket_id: 't1', actor: { type: 'user', id: 'x' }, at: '' })
    es.emit('label.changed', { project_id: 'p1', actor: { type: 'user', id: 'x' }, at: '' })
    await flushPromises()
    const bar = document.body.querySelector('[data-testid=new-events]') as HTMLButtonElement
    expect(bar.textContent).toContain('2 new events, show')
    expect(document.body.querySelectorAll('[data-feed-id]')).toHaveLength(1) // list untouched
    F.list.mockResolvedValue({ items: [row(3, { id: 'new' }), row(2, { id: 'old' })], next_cursor: null })
    bar.click()
    await flushPromises()
    expect([...document.body.querySelectorAll('[data-feed-id]')].map((r) => r.getAttribute('data-feed-id'))).toEqual(['new', 'old'])
    expect(document.body.querySelector('[data-testid=new-events]')).toBeNull()
  })
})
