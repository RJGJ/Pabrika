import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError } from '@/api/client'
import { labels as labelsApi } from '@/api/labels'
import { members as membersApi } from '@/api/members'
import { projects as projectsApi } from '@/api/projects'
import type { ApiEvent, Label, Member, ProjectDetail, Role, UpdateProjectInput } from '@/api/types'
import { getRouter } from '@/router/instance'
import { useAuthStore } from './auth'
import { useBoardStore } from './board'
import { useProjectsStore } from './projects'

export type SettingsLoadState = 'idle' | 'loading' | 'ready' | 'error' | 'no-access' | 'not-found'

/** Handle the events composable registers so the store can stop and restart the stream. */
export interface StreamControl {
  close(): void
  reopen(): void
}

/**
 * State for the project settings screen. It talks to the API modules directly and does not depend
 * on the board store's data (it only resets the board store when the project is deleted or left).
 *
 * Live-update hook for the coordinator (events composable):
 *  - `applyEvent(ev)`: feed every stream event here. Ticket and comment events are ignored.
 *    `project.updated` refetches the project, `label.changed` the labels, `member.changed` the
 *    members and the project (the role may have changed). A 404 on a project-level refetch sets
 *    `loadState = 'no-access'` (lost access), unless `leaving` is set.
 *  - `leaving`: true while this user is deleting or leaving the project; the lost-access path must
 *    stay silent (no dialog) while it is set.
 *  - `registerStream(ctl | null)`: optional; `deleteProject` and `leave` call `ctl.close()` before the
 *    request and `ctl.reopen()` if it fails.
 */
export const useProjectSettingsStore = defineStore('projectSettings', () => {
  const key = ref<string | null>(null)
  const project = ref<ProjectDetail | null>(null)
  const members = ref<Member[]>([])
  const labels = ref<Label[]>([])
  const loadState = ref<SettingsLoadState>('idle')
  const leaving = ref(false)
  let stream: StreamControl | null = null
  let loadSeq = 0
  let ac: AbortController | null = null

  const role = computed<Role | null>(() => project.value?.role ?? null)
  const isArchived = computed(() => !!project.value?.archived_at)
  const isOwner = computed(() => role.value === 'owner')
  const isViewer = computed(() => role.value === 'viewer')
  /** Owners edit the project and members, even when archived (they can unarchive or delete). */
  const canManageProject = computed(() => role.value === 'owner')
  /** Editors and owners manage labels, except in archived projects (read-only for everyone). */
  const canManageLabels = computed(() => (role.value === 'owner' || role.value === 'editor') && !isArchived.value)

  function registerStream(ctl: StreamControl | null): void {
    stream = ctl
  }

  async function load(projectKey: string): Promise<void> {
    ac?.abort()
    const mine = ++loadSeq
    const ctl = (ac = new AbortController())
    const sameProject = key.value?.toLowerCase() === projectKey.toLowerCase() && project.value !== null
    key.value = projectKey
    if (!sameProject) {
      project.value = null
      members.value = []
      labels.value = []
    }
    loadState.value = 'loading'
    try {
      const [p, m, l] = await Promise.all([
        projectsApi.get(projectKey, ctl.signal),
        membersApi.list(projectKey, ctl.signal),
        labelsApi.list(projectKey, ctl.signal),
      ])
      if (mine !== loadSeq) return
      project.value = p
      members.value = m
      labels.value = l
      loadState.value = 'ready'
    } catch (e) {
      if (mine !== loadSeq || ctl.signal.aborted) return
      if (e instanceof ApiError && e.status === 404) loadState.value = sameProject ? 'no-access' : 'not-found'
      else loadState.value = 'error'
    }
  }

  /** The key to use in API paths: the canonical key once loaded. */
  function pkey(): string {
    return project.value?.key ?? key.value ?? ''
  }

  /** A 404 on a project-level refetch means the project is gone or we were removed. */
  function noteLostAccess(e: unknown): void {
    if (e instanceof ApiError && e.status === 404 && !leaving.value) loadState.value = 'no-access'
  }

  async function reloadProject(): Promise<void> {
    try {
      const p = await projectsApi.get(pkey())
      project.value = p
      useProjectsStore().updateLocal(stripCounts(p))
    } catch (e) {
      noteLostAccess(e)
    }
  }

  async function reloadMembers(): Promise<void> {
    try {
      members.value = await membersApi.list(pkey())
    } catch (e) {
      noteLostAccess(e)
    }
  }

  async function reloadLabels(): Promise<void> {
    try {
      labels.value = await labelsApi.list(pkey())
    } catch (e) {
      noteLostAccess(e)
    }
  }

  /** Live updates (see the doc comment above). Never throws. */
  async function applyEvent(ev: Pick<ApiEvent, 'type' | 'project_id'>): Promise<void> {
    if (loadState.value !== 'ready' || !project.value || ev.project_id !== project.value.id) return
    switch (ev.type) {
      case 'project.updated':
        await reloadProject()
        break
      case 'label.changed':
        await reloadLabels()
        break
      case 'member.changed':
        await Promise.all([reloadMembers(), reloadProject()])
        break
      default:
        break // ticket and comment events do not matter here
    }
  }

  async function updateProject(patch: UpdateProjectInput): Promise<void> {
    const p = await projectsApi.update(pkey(), patch)
    if (project.value) project.value = { ...project.value, ...p, counts: project.value.counts }
    useProjectsStore().updateLocal(p)
  }

  async function addMember(email: string, memberRole: Role): Promise<void> {
    const m = await membersApi.add(pkey(), email.trim(), memberRole)
    members.value = sortMembers([...members.value.filter((x) => x.user.id !== m.user.id), m])
  }

  async function setMemberRole(userId: string, memberRole: Role): Promise<void> {
    const m = await membersApi.setRole(pkey(), userId, memberRole)
    members.value = sortMembers(members.value.map((x) => (x.user.id === userId ? m : x)))
    if (userId === useAuthStore().user?.id) await reloadProject() // own role changed
  }

  /** Remove another member. Removing yourself goes through `leave`. */
  async function removeMember(userId: string): Promise<void> {
    await membersApi.remove(pkey(), userId)
    members.value = members.value.filter((x) => x.user.id !== userId)
  }

  /** Run `fn` with the stream closed and `leaving` set; on failure restore both. */
  async function whileLeaving(fn: () => Promise<void>): Promise<void> {
    leaving.value = true
    stream?.close()
    try {
      await fn()
    } catch (e) {
      leaving.value = false
      stream?.reopen()
      throw e
    }
  }

  async function afterGone(id: string): Promise<void> {
    const projects = useProjectsStore()
    projects.remove(id)
    useBoardStore().reset()
    reset()
    await projects.fetch()
    await getRouter()?.replace('/')
  }

  /** Any member can leave (an owner removing themself is the same path). 409 `last_owner` if sole owner. */
  async function leave(): Promise<void> {
    const me = useAuthStore().user
    const p = project.value
    if (!me || !p) return
    await whileLeaving(() => membersApi.remove(p.key, me.id))
    await afterGone(p.id)
  }

  async function deleteProject(): Promise<void> {
    const p = project.value
    if (!p) return
    await whileLeaving(() => projectsApi.remove(p.key))
    await afterGone(p.id)
  }

  async function createLabel(name: string, color: string): Promise<void> {
    const l = await labelsApi.create(pkey(), name.trim(), color)
    labels.value = sortLabels([...labels.value, l])
  }

  async function updateLabel(id: string, patch: { name?: string; color?: string }): Promise<void> {
    const l = await labelsApi.update(id, patch)
    labels.value = sortLabels(labels.value.map((x) => (x.id === id ? l : x)))
  }

  async function deleteLabel(id: string): Promise<void> {
    await labelsApi.remove(id)
    labels.value = labels.value.filter((x) => x.id !== id)
  }

  function reset(): void {
    ac?.abort()
    ac = null
    loadSeq++
    key.value = null
    project.value = null
    members.value = []
    labels.value = []
    loadState.value = 'idle'
    leaving.value = false
    stream = null
  }

  return {
    key, project, members, labels, loadState, leaving,
    role, isArchived, isOwner, isViewer, canManageProject, canManageLabels,
    registerStream, load, reloadProject, reloadMembers, reloadLabels, applyEvent,
    updateProject, addMember, setMemberRole, removeMember, leave, deleteProject,
    createLabel, updateLabel, deleteLabel, reset,
  }
})

function stripCounts(p: ProjectDetail) {
  const { counts: _counts, ...rest } = p
  return rest
}

function sortMembers(list: Member[]): Member[] {
  // The server orders owners first, then display name; keep the same order after local edits.
  return [...list].sort(
    (a, b) => Number(b.role === 'owner') - Number(a.role === 'owner') || a.user.display_name.localeCompare(b.user.display_name),
  )
}

function sortLabels(list: Label[]): Label[] {
  return [...list].sort((a, b) => a.name.localeCompare(b.name))
}
