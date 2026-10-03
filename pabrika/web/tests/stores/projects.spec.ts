import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Project } from '@/api/types'

vi.mock('@/api/projects', () => ({ projects: { list: vi.fn(), create: vi.fn() } }))

import { projects as api } from '@/api/projects'
import { setRouter } from '@/router/instance'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'

const mocked = api as unknown as Record<string, ReturnType<typeof vi.fn>>
const proj = (key: string, name = key, archived = false): Project => ({
  id: 'id-' + key, key, name, description: '', archived_at: archived ? 'x' : null,
  created_at: '', updated_at: '', role: 'owner',
})

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  mocked.list.mockReset()
  mocked.create.mockReset()
})

describe('projects store', () => {
  it('fetch loads the list; byKey is case-insensitive', async () => {
    mocked.list.mockResolvedValue([proj('WEB'), proj('API')])
    const s = useProjectsStore()
    await s.fetch()
    expect(s.loaded).toBe(true)
    expect(s.byKey('web')?.key).toBe('WEB')
    expect(s.byKey('nope')).toBeUndefined()
  })
  it('records an error and stays unloaded on failure', async () => {
    mocked.list.mockRejectedValue(new Error('boom'))
    const s = useProjectsStore()
    await s.fetch()
    expect(s.loaded).toBe(false)
    expect(s.error).toBe('boom')
  })
  it('setIncludeArchived refetches with the flag', async () => {
    mocked.list.mockResolvedValue([])
    const s = useProjectsStore()
    await s.setIncludeArchived(true)
    expect(mocked.list).toHaveBeenCalledWith(true, expect.anything())
  })
  it('create adds to the list, remembers the key and navigates to the board', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<i/>' } },
        { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      ],
    })
    setRouter(router)
    mocked.create.mockResolvedValue(proj('NEW', 'New thing'))
    const s = useProjectsStore()
    await s.create({ key: 'NEW', name: 'New thing' })
    expect(s.list.map((p) => p.key)).toEqual(['NEW'])
    expect(s.lastProjectKey).toBe('NEW')
    expect(localStorage.getItem('pabrika.lastProjectKey')).toBe('NEW')
    expect(router.currentRoute.value.fullPath).toBe('/p/NEW')
  })
  it('create propagates API errors', async () => {
    mocked.create.mockRejectedValue(new Error('key_taken'))
    await expect(useProjectsStore().create({ key: 'WEB', name: 'x' })).rejects.toThrow('key_taken')
  })
  it('sidebarList includes the open archived project hidden by the toggle', () => {
    const s = useProjectsStore()
    s.list = [proj('WEB')]
    const board = useBoardStore()
    board.project = { ...proj('OLD', 'Old', true), counts: { backlog: 0, todo: 0, in_progress: 0, done: 0 } }
    expect(s.sidebarList.map((p) => p.key)).toEqual(['OLD', 'WEB'])
  })
  it('remove drops a project locally', () => {
    const s = useProjectsStore()
    s.list = [proj('WEB'), proj('API')]
    s.remove('id-WEB')
    expect(s.list.map((p) => p.key)).toEqual(['API'])
  })
})
