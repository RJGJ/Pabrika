import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { Label, Member, ProjectDetail, Status, TicketSummary } from '@/api/types'
import { STATUSES } from '@/api/types'
import type { ApiEvent } from '@/api/types'

export type LoadState = 'idle' | 'loading' | 'ready' | 'error' | 'no-access' | 'not-found'

const emptyColumns = (): Record<Status, string[]> => ({ backlog: [], todo: [], in_progress: [], done: [] })

/**
 * EXTENSION POINT (WP4 to WP6): the board store is typed here with its state and getters but has no
 * loading, drag or event logic yet. WP4 adds load/createTicket/updateTicket/moveTicket/refreshTicket,
 * WP6 adds applyEvent. Other code (projects store, auth.handleUnauthorized, the API client's
 * forbidden hook) only depends on `project`, `reset()` and `refetchProject()`.
 */
export const useBoardStore = defineStore('board', () => {
  const project = ref<ProjectDetail | null>(null)
  const members = ref<Member[]>([])
  const labels = ref<Label[]>([])
  const ticketsById = ref<Record<string, TicketSummary>>({})
  const columns = ref<Record<Status, string[]>>(emptyColumns())
  const loadState = ref<LoadState>('idle')
  const flashIds = ref<Set<string>>(new Set())
  const pendingMoves = ref<Set<string>>(new Set())
  const dragging = ref<{ column?: Status } | null>(null)
  const deferredEvents = ref<ApiEvent[]>([])
  const leaving = ref(false)
  const selectedRef = ref<string | null>(null)

  const role = computed(() => project.value?.role ?? null)
  const isArchived = computed(() => !!project.value?.archived_at)
  const isOwner = computed(() => role.value === 'owner')
  const isViewer = computed(() => role.value === 'viewer')
  const canEdit = computed(() => (role.value === 'owner' || role.value === 'editor') && !isArchived.value)
  const counts = computed(() => {
    const out = { backlog: 0, todo: 0, in_progress: 0, done: 0 }
    for (const s of STATUSES) out[s] = columns.value[s].length
    return out
  })

  /** Called by the API client after a 403 (the role may have changed). Implemented in WP4. */
  async function refetchProject(): Promise<void> {}

  function reset(): void {
    project.value = null
    members.value = []
    labels.value = []
    ticketsById.value = {}
    columns.value = emptyColumns()
    loadState.value = 'idle'
    flashIds.value = new Set()
    pendingMoves.value = new Set()
    dragging.value = null
    deferredEvents.value = []
    leaving.value = false
    selectedRef.value = null
  }

  return {
    project, members, labels, ticketsById, columns, loadState, flashIds, pendingMoves, dragging,
    deferredEvents, leaving, selectedRef,
    role, isArchived, isOwner, isViewer, canEdit, counts,
    refetchProject, reset,
  }
})
