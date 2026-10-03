import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import type { Label, Member, ProjectDetail, Role } from '@/api/types'

vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), update: vi.fn(), remove: vi.fn(), list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn(), add: vi.fn(), setRole: vi.fn(), remove: vi.fn() } }))
vi.mock('@/api/labels', () => ({ labels: { list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn() } }))

import { projects as projectsApi } from '@/api/projects'
import { members as membersApi } from '@/api/members'
import { labels as labelsApi } from '@/api/labels'
import { setRouter } from '@/router/instance'
import { useAuthStore } from '@/stores/auth'
import { useProjectSettingsStore } from '@/stores/projectSettings'

type M = Record<string, ReturnType<typeof vi.fn>>
const pApi = projectsApi as unknown as M
const mApi = membersApi as unknown as M
const lApi = labelsApi as unknown as M

const project = (role: Role = 'owner', archived = false): ProjectDetail => ({
  id: 'P1', key: 'WEB', name: 'Web', description: '', archived_at: archived ? '2026-01-01T00:00:00Z' : null,
  created_at: '', updated_at: '', role, counts: { backlog: 1, todo: 0, in_progress: 0, done: 0 },
})
const member = (id: string, name: string, role: Role = 'viewer'): Member => ({
  user: { id, email: `${id}@x.co`, display_name: name }, role, created_at: '',
})
const label = (id: string, name: string): Label => ({ id, project_id: 'P1', name, color: 'blue' })

function mockLoad(role: Role = 'owner', archived = false) {
  pApi.get.mockResolvedValue(project(role, archived))
  mApi.list.mockResolvedValue([member('u1', 'Ada', 'owner'), member('u2', 'Bob')])
  lApi.list.mockResolvedValue([label('l1', 'bug')])
}

beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [pApi, mApi, lApi]) for (const fn of Object.values(m)) fn.mockReset()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] })
  setRouter(router)
  useAuthStore().user = { id: 'u1', email: 'u1@x.co', display_name: 'Ada', created_at: '' }
})

describe('projectSettings load', () => {
  it('loads project, members and labels without touching tickets', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('web')
    expect(s.loadState).toBe('ready')
    expect(s.project?.key).toBe('WEB')
    expect(s.members).toHaveLength(2)
    expect(s.labels).toHaveLength(1)
    expect(pApi.get.mock.calls[0][0]).toBe('web')
  })
  it('first-load 404 is not-found; other failures are error', async () => {
    pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    mApi.list.mockResolvedValue([])
    lApi.list.mockResolvedValue([])
    const s = useProjectSettingsStore()
    await s.load('nope')
    expect(s.loadState).toBe('not-found')
    pApi.get.mockRejectedValue(new ApiError(500, 'internal', 'x'))
    await s.load('nope')
    expect(s.loadState).toBe('error')
  })
  it('reload of a loaded project that 404s is no-access', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    await s.load('WEB')
    expect(s.loadState).toBe('no-access')
  })
})

describe('permission getters (read-only matrix)', () => {
  const cases: [Role, boolean, boolean, boolean][] = [
    // role, archived, canManageProject, canManageLabels
    ['owner', false, true, true],
    ['editor', false, false, true],
    ['viewer', false, false, false],
    ['owner', true, true, false],
    ['editor', true, false, false],
    ['viewer', true, false, false],
  ]
  it.each(cases)('%s archived=%s', async (role, archived, manageProject, manageLabels) => {
    mockLoad(role, archived)
    const s = useProjectSettingsStore()
    await s.load('WEB')
    expect(s.canManageProject).toBe(manageProject)
    expect(s.canManageLabels).toBe(manageLabels)
  })
})

describe('members', () => {
  it('addMember inserts and keeps owners first', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    mApi.add.mockResolvedValue(member('u3', 'Aaron', 'editor'))
    await s.addMember(' u3@x.co ', 'editor')
    expect(mApi.add).toHaveBeenCalledWith('WEB', 'u3@x.co', 'editor')
    expect(s.members.map((m) => m.user.id)).toEqual(['u1', 'u3', 'u2'])
  })
  it('add errors propagate for inline display', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    mApi.add.mockRejectedValue(new ApiError(409, 'already_member', 'Already a member'))
    await expect(s.addMember('u2@x.co', 'viewer')).rejects.toMatchObject({ code: 'already_member' })
    expect(s.members).toHaveLength(2)
  })
  it('setMemberRole uses member.user.id in the path', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    mApi.setRole.mockResolvedValue(member('u2', 'Bob', 'editor'))
    await s.setMemberRole('u2', 'editor')
    expect(mApi.setRole).toHaveBeenCalledWith('WEB', 'u2', 'editor')
    expect(s.members.find((m) => m.user.id === 'u2')?.role).toBe('editor')
  })
  it('removeMember drops the row; last_owner leaves it in place and rejects', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    mApi.remove.mockResolvedValueOnce(undefined)
    await s.removeMember('u2')
    expect(s.members.map((m) => m.user.id)).toEqual(['u1'])
    mApi.remove.mockRejectedValueOnce(new ApiError(409, 'last_owner', 'A project needs at least one owner'))
    await expect(s.removeMember('u1')).rejects.toMatchObject({ code: 'last_owner' })
    expect(s.members).toHaveLength(1)
  })
})

describe('leave and delete', () => {
  it('leave sets leaving and closes the stream first, then resets and goes home', async () => {
    mockLoad('editor')
    const s = useProjectSettingsStore()
    await s.load('WEB')
    const order: string[] = []
    s.registerStream({ close: () => order.push('close'), reopen: () => order.push('reopen') })
    mApi.remove.mockImplementation(async () => {
      order.push('delete')
      expect(s.leaving).toBe(true)
    })
    pApi.list.mockResolvedValue([])
    await s.leave()
    expect(order).toEqual(['close', 'delete'])
    expect(mApi.remove).toHaveBeenCalledWith('WEB', 'u1')
    expect(s.loadState).toBe('idle')
  })
  it('a failed leave clears leaving and reopens the stream', async () => {
    mockLoad('owner')
    const s = useProjectSettingsStore()
    await s.load('WEB')
    const reopen = vi.fn()
    s.registerStream({ close: vi.fn(), reopen })
    mApi.remove.mockRejectedValue(new ApiError(409, 'last_owner', 'A project needs at least one owner'))
    await expect(s.leave()).rejects.toMatchObject({ code: 'last_owner' })
    expect(s.leaving).toBe(false)
    expect(reopen).toHaveBeenCalled()
    expect(s.loadState).toBe('ready')
  })
  it('deleteProject suppresses lost access while the request runs and failure reopens', async () => {
    mockLoad('owner')
    const s = useProjectSettingsStore()
    await s.load('WEB')
    const reopen = vi.fn()
    s.registerStream({ close: vi.fn(), reopen })
    pApi.remove.mockRejectedValue(new ApiError(500, 'internal', 'boom'))
    await expect(s.deleteProject()).rejects.toBeTruthy()
    expect(reopen).toHaveBeenCalled()
    expect(s.leaving).toBe(false)

    pApi.remove.mockImplementation(async () => {
      // member.changed lands while deleting: the 404 refetch must not flag lost access
      pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
      await s.applyEvent({ type: 'project.updated', project_id: 'P1' })
      expect(s.loadState).toBe('ready')
    })
    pApi.list.mockResolvedValue([])
    await s.deleteProject()
    expect(pApi.remove).toHaveBeenCalledWith('WEB')
  })
})

describe('labels', () => {
  it('create, update and delete keep the list sorted by name', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    lApi.create.mockResolvedValue(label('l2', 'aaa'))
    await s.createLabel(' aaa ', 'blue')
    expect(lApi.create).toHaveBeenCalledWith('WEB', 'aaa', 'blue')
    expect(s.labels.map((l) => l.name)).toEqual(['aaa', 'bug'])
    lApi.update.mockResolvedValue({ ...label('l2', 'zzz'), color: 'red' })
    await s.updateLabel('l2', { name: 'zzz', color: 'red' })
    expect(s.labels.map((l) => l.name)).toEqual(['bug', 'zzz'])
    lApi.remove.mockResolvedValue(undefined)
    await s.deleteLabel('l1')
    expect(s.labels.map((l) => l.id)).toEqual(['l2'])
  })
  it('label_exists propagates', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    lApi.create.mockRejectedValue(new ApiError(409, 'label_exists', 'exists'))
    await expect(s.createLabel('bug', 'red')).rejects.toMatchObject({ code: 'label_exists' })
  })
})

describe('applyEvent (fake events)', () => {
  const ev = (type: string, project_id = 'P1') => ({ type, project_id }) as never

  it('ignores ticket and comment events and other projects', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    pApi.get.mockClear(); mApi.list.mockClear(); lApi.list.mockClear()
    await s.applyEvent(ev('ticket.updated'))
    await s.applyEvent(ev('comment.added'))
    await s.applyEvent(ev('project.updated', 'OTHER'))
    expect(pApi.get).not.toHaveBeenCalled()
    expect(mApi.list).not.toHaveBeenCalled()
    expect(lApi.list).not.toHaveBeenCalled()
  })
  it('project.updated refetches only the project (role flips live)', async () => {
    mockLoad('editor')
    const s = useProjectSettingsStore()
    await s.load('WEB')
    expect(s.canManageProject).toBe(false)
    pApi.get.mockResolvedValue(project('owner'))
    mApi.list.mockClear()
    await s.applyEvent(ev('project.updated'))
    expect(s.canManageProject).toBe(true)
    expect(mApi.list).not.toHaveBeenCalled()
  })
  it('label.changed refetches labels', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    lApi.list.mockResolvedValue([label('l9', 'new')])
    await s.applyEvent(ev('label.changed'))
    expect(s.labels.map((l) => l.id)).toEqual(['l9'])
  })
  it('member.changed refetches members and project; a 404 is lost access', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    mApi.list.mockResolvedValue([member('u1', 'Ada', 'owner')])
    await s.applyEvent(ev('member.changed'))
    expect(s.members).toHaveLength(1)
    pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    mApi.list.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    await s.applyEvent(ev('member.changed'))
    expect(s.loadState).toBe('no-access')
  })
  it('lost access is suppressed while leaving', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    s.leaving = true
    pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    await s.applyEvent(ev('project.updated'))
    expect(s.loadState).toBe('ready')
  })
})

describe('updateProject', () => {
  it('merges the response, keeping counts', async () => {
    mockLoad()
    const s = useProjectSettingsStore()
    await s.load('WEB')
    const { counts: _c, ...rest } = project()
    pApi.update.mockResolvedValue({ ...rest, name: 'Renamed' })
    await s.updateProject({ name: 'Renamed' })
    expect(pApi.update).toHaveBeenCalledWith('WEB', { name: 'Renamed' })
    expect(s.project?.name).toBe('Renamed')
    expect(s.project?.counts.backlog).toBe(1)
  })
})
