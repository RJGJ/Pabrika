import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import type { Label, Member, ProjectDetail, Role } from '@/api/types'

vi.mock('@/api/projects', () => ({ projects: { get: vi.fn(), update: vi.fn(), remove: vi.fn(), list: vi.fn() } }))
vi.mock('@/api/members', () => ({ members: { list: vi.fn(), add: vi.fn(), setRole: vi.fn(), remove: vi.fn() } }))
vi.mock('@/api/labels', () => ({ labels: { list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { projects as projectsApi } from '@/api/projects'
import { members as membersApi } from '@/api/members'
import { labels as labelsApi } from '@/api/labels'
import { notify } from '@/lib/toast'
import { setRouter } from '@/router/instance'
import DangerZone from '@/components/settings/DangerZone.vue'
import GeneralSettings from '@/components/settings/GeneralSettings.vue'
import LabelsManager from '@/components/settings/LabelsManager.vue'
import MembersTable from '@/components/settings/MembersTable.vue'
import ProjectSettingsView from '@/views/ProjectSettingsView.vue'
import { useAuthStore } from '@/stores/auth'
import { useProjectSettingsStore } from '@/stores/projectSettings'

type M = Record<string, ReturnType<typeof vi.fn>>
const pApi = projectsApi as unknown as M
const mApi = membersApi as unknown as M
const lApi = labelsApi as unknown as M

const project = (role: Role, archived = false): ProjectDetail => ({
  id: 'P1', key: 'WEB', name: 'Web', description: 'desc', archived_at: archived ? '2026-01-01T00:00:00Z' : null,
  created_at: '', updated_at: '', role, counts: { backlog: 0, todo: 0, in_progress: 0, done: 0 },
})
const member = (id: string, name: string, role: Role = 'viewer'): Member => ({
  user: { id, email: `${id}@x.co`, display_name: name }, role, created_at: '',
})
const label = (id: string, name: string): Label => ({ id, project_id: 'P1', name, color: 'blue' })

const mounted: { unmount(): void }[] = []
function dlgButton(text: string): HTMLButtonElement {
  const d = document.body.querySelector('[role=dialog]')!
  return [...d.querySelectorAll('button')].find((b) => b.textContent?.trim() === text) as HTMLButtonElement
}
function typeInto(sel: string, v: string) {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = v
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

function seed(role: Role, archived = false) {
  const s = useProjectSettingsStore()
  s.project = project(role, archived)
  s.members = [member('u1', 'Ada', role === 'owner' ? 'owner' : role), member('u2', 'Bob', 'viewer')]
  s.labels = [label('l1', 'bug')]
  s.loadState = 'ready'
  return s
}

let router: ReturnType<typeof createRouter>
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [pApi, mApi, lApi]) for (const fn of Object.values(m)) fn.mockReset()
  vi.mocked(notify).mockReset()
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div>home</div>' } },
      { path: '/p/:key', name: 'board', component: { template: '<div>board</div>' } },
      { path: '/p/:key/settings', name: 'project-settings', component: ProjectSettingsView },
    ],
  })
  setRouter(router)
  useAuthStore().user = { id: 'u1', email: 'u1@x.co', display_name: 'Ada', created_at: '' }
})

const mountIt = (c: object) => {
  const w = mount(c, { global: { plugins: [router] }, attachTo: document.body })
  mounted.push(w)
  return w
}

describe('read-only matrix', () => {
  const rows: [string, Role, boolean, { general: boolean; members: boolean; labels: boolean }][] = [
    ['owner', 'owner', false, { general: true, members: true, labels: true }],
    ['editor', 'editor', false, { general: false, members: false, labels: true }],
    ['viewer', 'viewer', false, { general: false, members: false, labels: false }],
    ['archived owner', 'owner', true, { general: true, members: true, labels: false }],
    ['archived editor', 'editor', true, { general: false, members: false, labels: false }],
  ]
  it.each(rows)('%s', (_n, role, archived, exp) => {
    seed(role, archived)
    const g = mountIt(GeneralSettings)
    expect(g.find('#settings-name').exists()).toBe(exp.general)
    expect(g.find('[data-testid=general-readonly]').exists()).toBe(!exp.general)
    expect(g.find('[data-testid=danger-zone]').exists()).toBe(exp.general)
    expect(g.find('#settings-archived').exists()).toBe(exp.general)

    const m = mountIt(MembersTable)
    expect(m.find('[data-testid=add-member-form]').exists()).toBe(exp.members)
    expect(m.find('select[aria-label="Role for Bob"]').exists()).toBe(exp.members)
    expect(m.find('button[aria-label="Remove Bob"]').exists()).toBe(exp.members)
    expect(m.find('[data-testid=leave-project]').exists()).toBe(true) // any member can leave

    const l = mountIt(LabelsManager)
    expect(l.find('[data-testid=create-label-form]').exists()).toBe(exp.labels)
    expect(l.find('button[aria-label="Edit bug"]').exists()).toBe(exp.labels)
    expect(l.find('button[aria-label="Delete bug"]').exists()).toBe(exp.labels)
    expect(l.find('[data-testid=labels-readonly]').exists()).toBe(!exp.labels)
    if (archived) expect(l.text()).toContain('archived')
  })
})

describe('general', () => {
  it('saves name and description via PATCH', async () => {
    const s = seed('owner')
    const { counts: _c, ...rest } = project('owner')
    pApi.update.mockResolvedValue({ ...rest, name: 'New', description: 'd2' })
    const w = mountIt(GeneralSettings)
    await w.find('#settings-name').setValue('New')
    await w.find('#settings-description').setValue('d2')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(pApi.update).toHaveBeenCalledWith('WEB', { name: 'New', description: 'd2' })
    expect(s.project?.name).toBe('New')
  })
  it('rejects an empty name client-side and shows 422 field messages', async () => {
    seed('owner')
    const w = mountIt(GeneralSettings)
    await w.find('#settings-name').setValue('  ')
    await w.find('form').trigger('submit')
    expect(w.text()).toContain('Name must be 1 to 100')
    expect(pApi.update).not.toHaveBeenCalled()
    pApi.update.mockRejectedValue(new ApiError(422, 'validation_failed', 'bad', { name: 'Too long' }))
    await w.find('#settings-name').setValue('ok')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('Too long')
  })
})

describe('danger zone', () => {
  it('requires the key typed, deletes, and sets leaving so lost access stays silent', async () => {
    const s = seed('owner')
    const close = vi.fn()
    s.registerStream({ close, reopen: vi.fn() })
    pApi.list.mockResolvedValue([])
    let leavingDuringDelete = false
    pApi.remove.mockImplementation(async () => {
      leavingDuringDelete = s.leaving
    })
    const w = mountIt(DangerZone)
    await w.find('[data-testid=delete-project]').trigger('click')
    await flushPromises()
    const btn = () => document.body.querySelector('[data-testid=confirm-delete]') as HTMLButtonElement
    expect(btn().disabled).toBe(true)
    typeInto('#delete-confirm-key', 'NOPE')
    await flushPromises()
    expect(btn().disabled).toBe(true)
    typeInto('#delete-confirm-key', 'WEB')
    await flushPromises()
    expect(btn().disabled).toBe(false)
    btn().click()
    await flushPromises()
    expect(close).toHaveBeenCalled()
    expect(leavingDuringDelete).toBe(true)
    expect(pApi.remove).toHaveBeenCalledWith('WEB')
    expect(router.currentRoute.value.path).toBe('/')
  })
})

describe('members', () => {
  async function addMember(w: ReturnType<typeof mountIt>, email: string) {
    await w.find('#member-email').setValue(email)
    await w.find('[data-testid=add-member-form]').trigger('submit')
    await flushPromises()
  }
  it('adds by email with the chosen role', async () => {
    const s = seed('owner')
    mApi.add.mockResolvedValue(member('u3', 'Cy', 'editor'))
    const w = mountIt(MembersTable)
    await w.find('#member-role').setValue('editor')
    await addMember(w, 'u3@x.co')
    expect(mApi.add).toHaveBeenCalledWith('WEB', 'u3@x.co', 'editor')
    expect(s.members.some((m) => m.user.id === 'u3')).toBe(true)
  })
  it('shows 422 email and 409 already_member inline', async () => {
    seed('owner')
    const w = mountIt(MembersTable)
    mApi.add.mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'bad', { email: 'No account with this email' }))
    await addMember(w, 'ghost@x.co')
    expect(w.find('#member-email-error').text()).toContain('No account with this email')
    mApi.add.mockRejectedValueOnce(new ApiError(409, 'already_member', 'Already a member'))
    await addMember(w, 'u2@x.co')
    expect(w.find('#member-email-error').text()).toContain('Already a member')
  })
  it('changes a role with PATCH using the user id', async () => {
    seed('owner')
    mApi.setRole.mockResolvedValue(member('u2', 'Bob', 'editor'))
    const w = mountIt(MembersTable)
    await w.find('select[aria-label="Role for Bob"]').setValue('editor')
    await flushPromises()
    expect(mApi.setRole).toHaveBeenCalledWith('WEB', 'u2', 'editor')
  })
  it('removes with confirm; last_owner message is surfaced', async () => {
    const s = seed('owner')
    const w = mountIt(MembersTable)
    mApi.remove.mockResolvedValueOnce(undefined)
    await w.find('button[aria-label="Remove Bob"]').trigger('click')
    await flushPromises()
    expect(mApi.remove).not.toHaveBeenCalled() // needs the confirm click
    dlgButton('Remove').click()
    await flushPromises()
    expect(mApi.remove).toHaveBeenCalledWith('WEB', 'u2')
    expect(s.members.map((m) => m.user.id)).toEqual(['u1'])

    // owner removes self as the only owner: server refuses with last_owner
    mApi.remove.mockRejectedValueOnce(new ApiError(409, 'last_owner', 'A project needs at least one owner'))
    await w.find('button[aria-label="Remove Ada"]').trigger('click')
    await flushPromises()
    dlgButton('Remove').click()
    await flushPromises()
    expect(notify).toHaveBeenCalledWith('error', 'A project needs at least one owner')
    expect(s.leaving).toBe(false)
  })
  it('Leave project works for a viewer and suppresses lost access', async () => {
    const s = seed('viewer')
    s.members = [member('u1', 'Ada', 'viewer')]
    pApi.list.mockResolvedValue([])
    let leaving = false
    mApi.remove.mockImplementation(async () => {
      leaving = s.leaving
    })
    const w = mountIt(MembersTable)
    await w.find('[data-testid=leave-project]').trigger('click')
    await flushPromises()
    dlgButton('Leave project').click()
    await flushPromises()
    expect(mApi.remove).toHaveBeenCalledWith('WEB', 'u1')
    expect(leaving).toBe(true)
    expect(router.currentRoute.value.path).toBe('/')
  })
})

describe('labels', () => {
  it('creates a label with the chosen palette color', async () => {
    const s = seed('editor')
    lApi.create.mockResolvedValue({ ...label('l2', 'feature'), color: 'green' })
    const w = mountIt(LabelsManager)
    await w.find('#label-name').setValue('feature')
    await w.find('[data-testid=create-label-form] button[data-color=green]').trigger('click')
    await w.find('[data-testid=create-label-form]').trigger('submit')
    await flushPromises()
    expect(lApi.create).toHaveBeenCalledWith('WEB', 'feature', 'green')
    expect(s.labels.map((l) => l.name)).toEqual(['bug', 'feature'])
  })
  it('shows label_exists inline on create and on rename', async () => {
    seed('owner')
    const w = mountIt(LabelsManager)
    lApi.create.mockRejectedValue(new ApiError(409, 'label_exists', 'A label with this name exists'))
    await w.find('#label-name').setValue('bug')
    await w.find('[data-testid=create-label-form]').trigger('submit')
    await flushPromises()
    expect(w.find('#label-name-error').text()).toContain('exists')

    await w.find('button[aria-label="Edit bug"]').trigger('click')
    lApi.update.mockRejectedValue(new ApiError(409, 'label_exists', 'Name taken'))
    await w.find('#label-edit-l1').setValue('other')
    await w.find('[data-testid=label-l1] form').trigger('submit')
    await flushPromises()
    expect(w.find('#label-edit-l1-error').text()).toContain('Name taken')
  })
  it('deletes after confirm', async () => {
    const s = seed('owner')
    lApi.remove.mockResolvedValue(undefined)
    const w = mountIt(LabelsManager)
    await w.find('button[aria-label="Delete bug"]').trigger('click')
    await flushPromises()
    dlgButton('Delete').click()
    await flushPromises()
    expect(lApi.remove).toHaveBeenCalledWith('l1')
    expect(s.labels).toHaveLength(0)
  })
})

describe('ProjectSettingsView', () => {
  it('loads by route key and renders the tabs', async () => {
    pApi.get.mockResolvedValue(project('owner'))
    mApi.list.mockResolvedValue([member('u1', 'Ada', 'owner')])
    lApi.list.mockResolvedValue([])
    await router.push('/p/WEB/settings')
    const w = mountIt(ProjectSettingsView)
    await flushPromises()
    expect(pApi.get.mock.calls[0][0]).toBe('WEB')
    expect(w.text()).toContain('General')
    expect(w.text()).toContain('Members')
    expect(w.text()).toContain('Labels')
    expect(w.find('[data-testid=role-badge]').text()).toBe('owner')
  })
  it('shows the lost-access state after a 404 refetch event', async () => {
    pApi.get.mockResolvedValue(project('viewer'))
    mApi.list.mockResolvedValue([])
    lApi.list.mockResolvedValue([])
    await router.push('/p/WEB/settings')
    const w = mountIt(ProjectSettingsView)
    await flushPromises()
    pApi.get.mockRejectedValue(new ApiError(404, 'not_found', 'nf'))
    await useProjectSettingsStore().applyEvent({ type: 'member.changed', project_id: 'P1' })
    await flushPromises()
    expect(w.text()).toContain('You no longer have access')
  })
})
