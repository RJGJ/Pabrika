import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'

vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn() } }))
vi.mock('@/api/labels', () => ({ labels: { list: vi.fn() } }))
vi.mock('@/api/tickets', () => ({ tickets: { listAll: vi.fn(), get: vi.fn(), move: vi.fn() } }))
vi.mock('@/api/comments', () => ({ comments: { listAll: vi.fn(), activity: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { comments as commentsApi } from '@/api/comments'
import { members as membersApi } from '@/api/members'
import { labels as labelsApi } from '@/api/labels'
import { projects as projectsApi } from '@/api/projects'
import { tickets as ticketsApi } from '@/api/tickets'
import { setEventSourceFactory, type EventSourceLike } from '@/api/events'
import { ApiError } from '@/api/client'
import BoardView from '@/views/BoardView.vue'
import { routes } from '@/router'
import { setRouter } from '@/router/instance'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'
import { label, member, ME, project, ticket, type Mocked } from '../../stores/helpers'

const P = projectsApi as unknown as Mocked
const M = membersApi as unknown as Mocked
const L = labelsApi as unknown as Mocked
const T = ticketsApi as unknown as Mocked
const C = commentsApi as unknown as Mocked

class FakeES implements EventSourceLike {
  static last: FakeES | null = null
  readyState = 1
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  constructor(public url: string) {
    FakeES.last = this
  }
  addEventListener() {}
  close() {
    this.readyState = 2
  }
}

let wrapper: VueWrapper | null = null

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      ...routes.filter((r) => r.name === 'board' || r.name === 'ticket'),
      { path: '/', name: 'home', component: { template: '<i>home</i>' } },
      { path: '/p/:key/settings', name: 'project-settings', component: { template: '<i/>' } },
    ],
  })
  setRouter(router)
  await router.push(path)
  wrapper = mount({ render: () => h(RouterView) }, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { router }
}

beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [P, M, L, T, C]) for (const f of Object.values(m)) f.mockReset()
  FakeES.last = null
  setEventSourceFactory((url) => new FakeES(url))
  useAuthStore().user = ME
  P.get.mockResolvedValue(project())
  M.list.mockResolvedValue([member('u-me')])
  L.list.mockResolvedValue([label('l1')])
  T.listAll.mockResolvedValue([ticket(1), ticket(2, { status: 'done' })])
  P.list.mockResolvedValue([])
  T.get.mockResolvedValue({ ...ticket(1), description: '' })
  C.listAll.mockResolvedValue([])
  C.activity.mockResolvedValue({ items: [], next_cursor: null })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('BoardView', () => {
  it('renders four columns with the project header', async () => {
    await mountAt('/p/WEB')
    expect(document.body.textContent).toContain('Backlog')
    expect(document.body.textContent).toContain('To do')
    expect(document.body.textContent).toContain('In progress')
    expect(document.body.textContent).toContain('Done')
    expect(document.body.textContent).toContain('Web')
    expect(document.querySelectorAll('[data-column]')).toHaveLength(4)
  })

  it('normalizes the key: /p/web becomes /p/WEB, and the panel route keeps its number', async () => {
    const { router } = await mountAt('/p/web/t/2?q=x')
    expect(router.currentRoute.value.fullPath).toBe('/p/WEB/t/2?q=x')
    expect(P.get).toHaveBeenCalledTimes(1) // normalizing is not a reload
  })

  it('opening the panel (same component, same project) does not reload the board or reopen the stream', async () => {
    const { router } = await mountAt('/p/WEB')
    const stream = FakeES.last
    const loads = T.listAll.mock.calls.length
    await router.push('/p/WEB/t/1')
    await flushPromises()
    expect(T.listAll.mock.calls.length).toBe(loads)
    expect(FakeES.last).toBe(stream)
  })

  it('opens the event stream after the board is ready', async () => {
    await mountAt('/p/WEB')
    expect(FakeES.last?.url).toBe('/api/v1/projects/WEB/events')
  })

  it('shows not-found with a link home for an unknown project', async () => {
    P.get.mockRejectedValue(new ApiError(404, 'not_found', 'nope'))
    await mountAt('/p/NOPE')
    expect(document.body.textContent).toContain('Project not found')
    expect(document.body.textContent).toContain('Go home')
  })

  it('shows an inline error with Retry on a load failure', async () => {
    T.listAll.mockRejectedValue(new ApiError(0, 'network', 'Network error'))
    await mountAt('/p/WEB')
    expect(document.body.textContent).toContain("Couldn't load the board")
    T.listAll.mockResolvedValue([])
    const retry = [...document.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!
    retry.click()
    await flushPromises()
    expect(document.querySelectorAll('[data-column]')).toHaveLength(4)
  })

  it('shows the archived banner and hides drag affordances', async () => {
    P.get.mockResolvedValue(project({ archived_at: '2026-01-01T00:00:00Z' }))
    await mountAt('/p/WEB')
    expect(document.body.textContent).toContain('archived and read-only')
    expect(document.body.textContent).toContain('Unarchive')
    expect(document.body.textContent).not.toContain('Add ticket')
  })

  it('viewer mode: no Add ticket, View only badge', async () => {
    P.get.mockResolvedValue(project({ role: 'viewer' }))
    await mountAt('/p/WEB')
    expect(document.body.textContent).not.toContain('Add ticket')
    expect(document.body.textContent).toContain('View only')
  })

  it('lost access: dialog, project removed from the sidebar list, then home on dismiss', async () => {
    const { router } = await mountAt('/p/WEB')
    const projects = useProjectsStore()
    projects.list = [{ ...project(), counts: undefined } as never]
    const board = useBoardStore()
    P.get.mockRejectedValue(new ApiError(404, 'not_found', 'nope'))
    await board.refetchProject()
    await flushPromises()
    expect(document.body.textContent).toContain('You no longer have access to this project')
    expect(projects.list).toHaveLength(0)
    expect(FakeES.last?.readyState).toBe(2) // stream closed
    const btn = [...document.querySelectorAll('button')].find((b) => b.textContent === 'Back to projects')!
    btn.click()
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/')
    expect(board.lostAccess).toBe(false)
  })
})
