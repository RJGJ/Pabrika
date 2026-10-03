import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { projects as projectsApi } from '@/api/projects'
import type { CreateProjectInput, Project } from '@/api/types'
import { safeGet, safeSet } from '@/lib/storage'
import { getRouter } from '@/router/instance'
import { useBoardStore } from './board'

const LAST_KEY = 'pabrika.lastProjectKey'

export const useProjectsStore = defineStore('projects', () => {
  const list = ref<Project[]>([])
  const includeArchived = ref(false)
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<string | null>(null)
  const lastProjectKey = ref<string | null>(safeGet(LAST_KEY))
  let inflight: AbortController | null = null

  function byKey(key: string): Project | undefined {
    const k = key.toLowerCase()
    return list.value.find((p) => p.key.toLowerCase() === k)
  }

  /** Projects shown in the sidebar: the list plus the open project when it is archived and hidden. */
  const sidebarList = computed<Project[]>(() => {
    const open = useBoardStore().project
    if (open && !list.value.some((p) => p.id === open.id)) {
      const { counts: _counts, ...rest } = open as Project & { counts?: unknown }
      return [...list.value, rest as Project].sort((a, b) => a.name.localeCompare(b.name) || a.key.localeCompare(b.key))
    }
    return list.value
  })

  async function fetch(): Promise<void> {
    inflight?.abort()
    const ac = (inflight = new AbortController())
    loading.value = true
    error.value = null
    try {
      const items = await projectsApi.list(includeArchived.value, ac.signal)
      if (ac.signal.aborted) return
      list.value = items
      loaded.value = true
    } catch (e) {
      if (ac.signal.aborted) return
      error.value = e instanceof Error ? e.message : 'Failed to load projects'
    } finally {
      if (inflight === ac) {
        loading.value = false
        inflight = null
      }
    }
  }

  async function setIncludeArchived(v: boolean): Promise<void> {
    includeArchived.value = v
    await fetch()
  }

  function setLastProjectKey(key: string): void {
    lastProjectKey.value = key
    safeSet(LAST_KEY, key)
  }

  /** Create, add to the list and navigate to the new board. Errors propagate to the dialog. */
  async function create(input: CreateProjectInput): Promise<Project> {
    const p = await projectsApi.create(input)
    updateLocal(p)
    setLastProjectKey(p.key)
    await getRouter()?.push({ name: 'board', params: { key: p.key } })
    return p
  }

  function updateLocal(p: Project): void {
    const i = list.value.findIndex((x) => x.id === p.id)
    if (i >= 0) list.value[i] = p
    else if (!p.archived_at || includeArchived.value) list.value.push(p)
    list.value.sort((a, b) => a.name.localeCompare(b.name) || a.key.localeCompare(b.key))
  }

  function remove(id: string): void {
    list.value = list.value.filter((p) => p.id !== id)
  }

  function reset(): void {
    inflight?.abort()
    inflight = null
    list.value = []
    loaded.value = false
    loading.value = false
    error.value = null
    includeArchived.value = false
  }

  return {
    list, includeArchived, loading, loaded, error, lastProjectKey, sidebarList,
    byKey, fetch, setIncludeArchived, setLastProjectKey, create, updateLocal, remove, reset,
  }
})
