import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { ApiError } from '@/api/client'
import { labels as labelsApi } from '@/api/labels'
import { members as membersApi } from '@/api/members'
import { projects as projectsApi } from '@/api/projects'
import { tickets as ticketsApi } from '@/api/tickets'
import type {
  ApiEvent, Label, Member, MoveBody, ProjectDetail, Status, Ticket, TicketSummary, UpdateTicketInput,
} from '@/api/types'
import { STATUSES } from '@/api/types'
import { emptyFilters, filtersActive as hasFilters, matchesFilters, type BoardFilters } from '@/lib/filters'
import { computePlacement, type Placement } from '@/lib/placement'
import { notify } from '@/lib/toast'
import { getRouter } from '@/router/instance'
import { useAuthStore } from './auth'
import { useProjectsStore } from './projects'

export type LoadState = 'idle' | 'loading' | 'ready' | 'error' | 'no-access' | 'not-found'
export type LiveState = 'idle' | 'live' | 'reconnecting'

interface Parts {
  project?: boolean
  members?: boolean
  labels?: boolean
  tickets?: boolean
}
const ALL_PARTS: Parts = { project: true, members: true, labels: true, tickets: true }
const mergeParts = (a: Parts | null, b: Parts): Parts => ({
  project: a?.project || b.project,
  members: a?.members || b.members,
  labels: a?.labels || b.labels,
  tickets: a?.tickets || b.tickets,
})

const FLASH_MS = 1500
const emptyColumns = (): Record<Status, string[]> => ({ backlog: [], todo: [], in_progress: [], done: [] })
const emptyKeys = (): Record<Status, number> => ({ backlog: 0, todo: 0, in_progress: 0, done: 0 })

/** Optimistic (not yet saved) cards carry a client-side id. */
export const isTempId = (id: string): boolean => id.startsWith('tmp_')

function toSummary(t: Ticket | TicketSummary): TicketSummary {
  const { description: _d, renumbered: _r, ...rest } = t as Ticket & { renumbered?: boolean }
  return rest
}

function byPosition(a: TicketSummary, b: TicketSummary): number {
  if (a.position < b.position) return -1
  if (a.position > b.position) return 1
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

interface MoveJob {
  id: string
  ref: string
  fromStatus: Status
  toStatus: Status
  placement: Placement
  snapshot: { ticket: TicketSummary; cols: Partial<Record<Status, string[]>> }
  resolve: (ok: boolean) => void
}

export const useBoardStore = defineStore('board', () => {
  const project = ref<ProjectDetail | null>(null)
  const members = ref<Member[]>([])
  const labels = ref<Label[]>([])
  const ticketsById = ref<Record<string, TicketSummary>>({})
  const columns = ref<Record<Status, string[]>>(emptyColumns())
  /** Bumped per column to force the Sortable lists to re-sync from the store (rollback, reload). */
  const columnKeys = ref<Record<Status, number>>(emptyKeys())
  const loadState = ref<LoadState>('idle')
  const error = ref<string | null>(null)
  const flashIds = ref<Set<string>>(new Set())
  const pendingMoves = ref<Set<string>>(new Set())
  const dragging = ref<{ column?: Status } | null>(null)
  const deferredEvents = ref<ApiEvent[]>([])
  const leaving = ref(false)
  const selectedRef = ref<string | null>(null)
  const lostAccess = ref(false)
  const live = ref<LiveState>('idle')
  const ticketsLoaded = ref(false)
  const filters = ref<BoardFilters>(emptyFilters())
  /** Latest full ticket (with description) seen through a refresh, update or move; the panel follows it. */
  const fullById = shallowRef<Record<string, Ticket>>({})
  /** Bumped when a comment event names the ticket (the open panel refetches comments and activity). */
  const commentVersions = ref<Record<string, number>>({})

  let currentKey: string | null = null
  let everLoaded = false
  let loadSeq = 0
  let loadAC: AbortController | null = null
  let loadPromise: Promise<void> | null = null
  let rerun: Parts | null = null
  let deferredParts: Parts | null = null
  let tempSeq = 0
  const temps = new Map<string, TicketSummary>()
  const mutations = new Map<string, number>()
  const inflightRefresh = new Map<string, { promise: Promise<boolean>; again: boolean }>()
  const refreshAfterSettle = new Set<string>()
  const flashTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const queue: MoveJob[] = []
  let pumping = false

  // ---- getters ----
  const role = computed(() => project.value?.role ?? null)
  const isArchived = computed(() => !!project.value?.archived_at)
  const isOwner = computed(() => role.value === 'owner')
  const isViewer = computed(() => role.value === 'viewer')
  const canEdit = computed(() => (role.value === 'owner' || role.value === 'editor') && !isArchived.value)
  const counts = computed(() => {
    const out = { backlog: 0, todo: 0, in_progress: 0, done: 0 }
    for (const s of STATUSES) out[s] = columns.value[s].filter((id) => !isTempId(id)).length
    return out
  })
  const filtersActive = computed(() => hasFilters(filters.value))
  const filteredColumns = computed(() => {
    const meId = useAuthStore().user?.id ?? null
    const out = emptyColumns()
    for (const s of STATUSES) {
      out[s] = columns.value[s].filter((id) => {
        const t = ticketsById.value[id]
        return !!t && (isTempId(id) || matchesFilters(t, filters.value, meId))
      })
    }
    return out
  })
  const filteredCounts = computed(() => {
    const out = { backlog: 0, todo: 0, in_progress: 0, done: 0 }
    for (const s of STATUSES) out[s] = filteredColumns.value[s].filter((id) => !isTempId(id)).length
    return out
  })

  function ticketByNumber(n: number): TicketSummary | undefined {
    return Object.values(ticketsById.value).find((t) => t.number === n && !isTempId(t.id))
  }

  function setFilters(f: BoardFilters): void {
    filters.value = f
  }

  const isMine = (e: ApiEvent): boolean => {
    const me = useAuthStore().user
    return !!me && e.actor.type === 'user' && e.actor.id === me.id
  }

  const busy = (): boolean => dragging.value !== null || queue.length > 0 || pumping

  function bumpKeys(...statuses: Status[]): void {
    const next = { ...columnKeys.value }
    for (const s of statuses) next[s]++
    columnKeys.value = next
  }
  const bumpAllKeys = () => bumpKeys(...STATUSES)

  const mutationOf = (id: string): number => mutations.get(id) ?? 0
  const bumpMutation = (id: string): void => void mutations.set(id, mutationOf(id) + 1)

  // ---- loading ----

  function matchesKey(key: string): boolean {
    if (currentKey === null) return false
    const k = key.toLowerCase()
    if (k === currentKey.toLowerCase()) return true
    const p = project.value
    return !!p && (key === p.id || k === p.key.toLowerCase())
  }

  function applyTicketList(list: TicketSummary[]): void {
    const byId: Record<string, TicketSummary> = {}
    const cols = emptyColumns()
    for (const t of [...list].sort(byPosition)) {
      byId[t.id] = toSummary(t)
      cols[t.status].push(t.id)
    }
    for (const tmp of temps.values()) {
      byId[tmp.id] = tmp
      cols[tmp.status].push(tmp.id)
    }
    ticketsById.value = byId
    columns.value = cols
    ticketsLoaded.value = true
    bumpAllKeys()
  }

  function handleLoadError(e: unknown, quiet: boolean): void {
    if (e instanceof ApiError && e.status === 404) {
      if (everLoaded) handleLostAccess()
      else loadState.value = 'not-found'
      return
    }
    if (quiet && everLoaded) return // keep the current UI
    loadState.value = 'error'
    error.value = e instanceof Error ? e.message : 'Failed to load the board'
  }

  function startLoad(parts: Parts, quiet: boolean): Promise<void> {
    const seq = loadSeq
    const key = currentKey as string
    const ac = new AbortController()
    loadAC = ac
    if (!quiet) {
      loadState.value = 'loading'
      error.value = null
    }
    let self: Promise<void> | null = null
    const p: Promise<void> = (async () => {
      try {
        const [proj, mem, lab, tix] = await Promise.all([
          parts.project ? projectsApi.get(key, ac.signal) : undefined,
          parts.members ? membersApi.list(key, ac.signal) : undefined,
          parts.labels ? labelsApi.list(key, ac.signal) : undefined,
          parts.tickets ? ticketsApi.listAll(key, ac.signal) : undefined,
        ])
        if (seq !== loadSeq || ac.signal.aborted || lostAccess.value) return
        if (proj) project.value = proj
        if (mem) members.value = mem
        if (lab) labels.value = lab
        if (tix) applyTicketList(tix)
        everLoaded = true
        if (loadState.value !== 'ready' && project.value && (parts.project || everLoaded)) loadState.value = 'ready'
        error.value = null
      } catch (e) {
        if (seq !== loadSeq || ac.signal.aborted) return
        handleLoadError(e, quiet)
      } finally {
        if (loadPromise === self) {
          loadPromise = null
          loadAC = null
          if (rerun && seq === loadSeq) {
            const r = rerun
            rerun = null
            scheduleQuiet(r)
          }
        }
      }
    })()
    self = p
    loadPromise = p
    return p
  }

  /** Quiet reload honoring the drag/move deferral and coalescing with an in-flight load. */
  function scheduleQuiet(parts: Parts): Promise<void> {
    if (currentKey === null) return Promise.resolve()
    if (busy()) {
      deferredParts = mergeParts(deferredParts, parts)
      return Promise.resolve()
    }
    if (loadPromise) {
      rerun = mergeParts(rerun, parts)
      return loadPromise
    }
    return startLoad(parts, true)
  }

  /**
   * Load the project (members, labels and, unless `tickets: false`, all tickets) by route key or id.
   * `quiet` keeps the current UI (no skeleton); it is deferred while a drag or move is in progress.
   */
  async function load(key: string, opts: { quiet?: boolean; tickets?: boolean } = {}): Promise<void> {
    const parts: Parts = { ...ALL_PARTS, tickets: opts.tickets !== false }
    if (!matchesKey(key)) {
      reset()
      currentKey = key
    }
    if (opts.quiet) return scheduleQuiet(parts)
    if (loadPromise) {
      rerun = mergeParts(rerun, parts)
      return loadPromise
    }
    return startLoad(parts, false)
  }

  /** Quiet partial reload (events). */
  function reload(parts: Parts): Promise<void> {
    return scheduleQuiet(parts)
  }

  /** Called by the API client after a 403 (the role may have changed). */
  async function refetchProject(): Promise<void> {
    if (currentKey === null) return
    await reload({ project: true })
  }

  function handleLostAccess(): void {
    if (leaving.value) return
    loadAC?.abort()
    lostAccess.value = true
    loadState.value = 'no-access'
    if (project.value) useProjectsStore().remove(project.value.id)
  }

  /** The LostAccessDialog was dismissed: leave the board. */
  function acknowledgeLostAccess(): void {
    reset()
    void getRouter()?.replace('/')
  }

  // ---- ticket slotting ----

  function removeFromColumns(id: string): void {
    const cols = { ...columns.value }
    for (const s of STATUSES) {
      if (cols[s].includes(id)) cols[s] = cols[s].filter((x) => x !== id)
    }
    columns.value = cols
  }

  function removeLocal(id: string): void {
    removeFromColumns(id)
    const { [id]: _gone, ...rest } = ticketsById.value
    ticketsById.value = rest
    if (pendingMoves.value.has(id)) {
      const next = new Set(pendingMoves.value)
      next.delete(id)
      pendingMoves.value = next
    }
    if (fullById.value[id]) {
      const { [id]: _f, ...r } = fullById.value
      fullById.value = r
    }
  }

  /** Insert or replace a ticket and slot it into its column by position. */
  function upsert(t: Ticket | TicketSummary): void {
    const s = toSummary(t)
    if ('description' in t) fullById.value = { ...fullById.value, [t.id]: t as Ticket }
    ticketsById.value = { ...ticketsById.value, [s.id]: s }
    removeFromColumns(s.id)
    const col = [...columns.value[s.status]]
    let at = col.length
    for (let i = 0; i < col.length; i++) {
      const other = ticketsById.value[col[i]]
      if (other && byPosition(s, other) < 0) {
        at = i
        break
      }
    }
    col.splice(at, 0, s.id)
    columns.value = { ...columns.value, [s.status]: col }
  }

  function applyPlacementLocal(id: string, toStatus: Status, placement: Placement): void {
    const t = ticketsById.value[id]
    if (!t) return
    removeFromColumns(id)
    const col = [...columns.value[toStatus]]
    let idx: number
    if (placement.after) {
      const i = col.indexOf(placement.after)
      idx = i >= 0 ? i + 1 : col.length
    } else if (placement.before) {
      const i = col.indexOf(placement.before)
      idx = i >= 0 ? i : 0
    } else idx = placement.place === 'bottom' ? col.length : 0
    col.splice(idx, 0, id)
    const prev = ticketsById.value[col[idx - 1]]?.position
    const next = ticketsById.value[col[idx + 1]]?.position
    const finite = (n: number | undefined): n is number => n !== undefined && Number.isFinite(n)
    const position =
      finite(prev) && finite(next) ? (prev + next) / 2 : finite(prev) ? prev + 1024 : finite(next) ? next - 1024 : 1024
    columns.value = { ...columns.value, [toStatus]: col }
    ticketsById.value = { ...ticketsById.value, [id]: { ...t, status: toStatus, position } }
  }

  // ---- ticket mutations ----

  function failToast(e: unknown, prefix: string): void {
    const msg = e instanceof ApiError ? e.message : 'Unexpected error'
    notify('error', `${prefix}: ${msg}`)
  }

  async function createTicket(status: Status, title: string): Promise<Ticket> {
    const key = project.value?.key ?? currentKey
    if (!key) throw new Error('No project is open')
    const id = `tmp_${++tempSeq}`
    const tmp: TicketSummary = {
      id, ref: '', project_id: project.value?.id ?? '', project_key: project.value?.key ?? key, number: 0,
      title, status, priority: 'medium', assignee: null, labels: [], position: Infinity, due_date: null,
      comment_count: 0, created_at: '', updated_at: '',
    }
    temps.set(id, tmp)
    ticketsById.value = { ...ticketsById.value, [id]: tmp }
    columns.value = { ...columns.value, [status]: [...columns.value[status], id] }
    try {
      const t = await ticketsApi.create(key, { title, status })
      temps.delete(id)
      removeLocal(id)
      upsert(t)
      return t
    } catch (e) {
      temps.delete(id)
      removeLocal(id)
      if (!(e instanceof ApiError && (e.status === 422 || e.status === 403))) failToast(e, 'Couldn\'t create the ticket')
      if (e instanceof ApiError && e.code === 'project_archived') void refetchProject()
      throw e
    }
  }

  async function updateTicket(id: string, patch: UpdateTicketInput): Promise<Ticket> {
    const before = ticketsById.value[id]
    if (!before) throw new Error('Unknown ticket')
    bumpMutation(id)
    const next: TicketSummary = { ...before }
    if (patch.title !== undefined) next.title = patch.title
    if (patch.priority !== undefined) next.priority = patch.priority
    if (patch.due_date !== undefined) next.due_date = patch.due_date
    if (patch.assignee !== undefined) {
      const m = members.value.find((x) => x.user.id === patch.assignee)
      next.assignee = patch.assignee === null || !m ? null : { id: m.user.id, display_name: m.user.display_name, email: m.user.email }
    }
    if (patch.labels !== undefined) {
      next.labels = patch.labels.map((lid) => labels.value.find((l) => l.id === lid)).filter((l): l is Label => !!l)
    }
    ticketsById.value = { ...ticketsById.value, [id]: next }
    try {
      const res = await ticketsApi.update(id, patch)
      bumpMutation(id)
      if (ticketsById.value[id]) {
        ticketsById.value = { ...ticketsById.value, [id]: toSummary(res) }
        fullById.value = { ...fullById.value, [id]: res }
      }
      return res
    } catch (e) {
      bumpMutation(id)
      if (ticketsById.value[id]) ticketsById.value = { ...ticketsById.value, [id]: before }
      if (!(e instanceof ApiError && (e.status === 422 || e.status === 403))) failToast(e, `Couldn't update ${before.ref}`)
      if (e instanceof ApiError && e.code === 'project_archived') void refetchProject()
      throw e
    }
  }

  async function deleteTicket(id: string): Promise<void> {
    const t = ticketsById.value[id]
    bumpMutation(id)
    try {
      await ticketsApi.remove(id)
    } catch (e) {
      failToast(e, `Couldn't delete ${t?.ref ?? 'the ticket'}`)
      throw e
    }
    removeLocal(id)
  }

  // ---- optimistic moves ----

  function placementValid(job: MoveJob): boolean {
    const { after, before } = job.placement
    const n = after ?? before
    if (!n) return true
    return n !== job.id && columns.value[job.toStatus].includes(n) && !!ticketsById.value[n]
  }

  function recomputePlacement(job: MoveJob): Placement {
    const col = columns.value[job.toStatus].filter((x) => !isTempId(x))
    const idx = col.indexOf(job.id)
    const without = col.filter((x) => x !== job.id)
    return computePlacement(without, idx < 0 ? without.length : idx)
  }

  function clearPending(id: string): void {
    if (queue.some((j) => j.id === id)) return
    if (!pendingMoves.value.has(id)) return
    const next = new Set(pendingMoves.value)
    next.delete(id)
    pendingMoves.value = next
  }

  function restoreSnapshot(job: MoveJob): void {
    if (!ticketsById.value[job.id]) return
    const cols = { ...columns.value }
    for (const s of Object.keys(job.snapshot.cols) as Status[]) {
      cols[s] = (job.snapshot.cols[s] as string[]).filter((id) => !!ticketsById.value[id] || id === job.id)
    }
    columns.value = cols
    ticketsById.value = { ...ticketsById.value, [job.id]: job.snapshot.ticket }
    bumpKeys(job.fromStatus, job.toStatus)
  }

  async function runMove(job: MoveJob): Promise<boolean> {
    if (!ticketsById.value[job.id]) return false // deleted meanwhile
    const placement = placementValid(job) ? job.placement : recomputePlacement(job)
    const body: MoveBody = { status: job.toStatus, ...placement }
    try {
      const res = await ticketsApi.move(job.id, body)
      bumpMutation(job.id)
      const laterSame = queue.some((j) => j !== job && j.id === job.id)
      if (ticketsById.value[job.id] && !laterSame) {
        upsert(res)
        bumpKeys(job.fromStatus, job.toStatus)
      } else if ('description' in res) {
        fullById.value = { ...fullById.value, [res.id]: res }
      }
      if (res.renumbered) void reload({ ...ALL_PARTS, tickets: true })
      return true
    } catch (e) {
      bumpMutation(job.id)
      // Later queued moves were built on this one: roll them back too (newest first), then this one.
      const dropped = queue.splice(0).filter((j) => j !== job)
      queue.push(job) // the pump shifts it
      for (const j of dropped.reverse()) {
        restoreSnapshot(j)
        j.resolve(false)
        if (j.id !== job.id && pendingMoves.value.has(j.id)) {
          const next = new Set(pendingMoves.value)
          next.delete(j.id)
          pendingMoves.value = next
        }
      }
      const err = e instanceof ApiError ? e : null
      if (err?.status === 404) {
        removeLocal(job.id)
        notify('error', `${job.ref} no longer exists`)
        void refetchProject()
        return false
      }
      restoreSnapshot(job)
      if (err?.status !== 403) failToast(e, `Couldn't move ${job.ref}`)
      if (err && (err.status === 422 || err.code === 'project_archived')) {
        void reload({ ...ALL_PARTS, tickets: true })
      }
      return false
    }
  }

  async function pump(): Promise<void> {
    if (pumping) return
    pumping = true
    try {
      while (queue.length) {
        const job = queue[0]
        let ok = false
        try {
          ok = await runMove(job)
        } finally {
          if (queue[0] === job) queue.shift()
          clearPending(job.id)
          job.resolve(ok)
        }
      }
    } finally {
      pumping = false
    }
    settle()
  }

  /**
   * Optimistically move a ticket (the placement was computed from the target column without the
   * moved id). Requests run through one FIFO queue. Resolves true when the server accepted the move.
   */
  function moveTicket(id: string, toStatus: Status, placement: Placement): Promise<boolean> {
    const t = ticketsById.value[id]
    if (!t || isTempId(id)) return Promise.resolve(false)
    const fromStatus = t.status
    const cols: MoveJob['snapshot']['cols'] = { [fromStatus]: [...columns.value[fromStatus]] }
    cols[toStatus] = [...columns.value[toStatus]]
    bumpMutation(id)
    applyPlacementLocal(id, toStatus, placement)
    pendingMoves.value = new Set(pendingMoves.value).add(id)
    return new Promise<boolean>((resolve) => {
      queue.push({ id, ref: t.ref, fromStatus, toStatus, placement, snapshot: { ticket: { ...t }, cols }, resolve })
      void pump()
    })
  }

  function startDrag(column: Status): void {
    dragging.value = { column }
  }
  function endDrag(): void {
    dragging.value = null
    settle()
  }

  /** Flush deferred work (events, refreshes, reloads) once no drag or move is active. */
  function settle(): void {
    if (busy()) return
    const refresh = [...refreshAfterSettle]
    refreshAfterSettle.clear()
    const events = deferredEvents.value
    deferredEvents.value = []
    const parts = deferredParts
    deferredParts = null
    for (const e of events) void applyEvent(e)
    for (const id of refresh) void refreshTicket(id)
    if (parts) void scheduleQuiet(parts)
  }

  // ---- live updates ----

  function flash(id: string): void {
    flashIds.value = new Set(flashIds.value).add(id)
    const old = flashTimers.get(id)
    if (old) clearTimeout(old)
    flashTimers.set(
      id,
      setTimeout(() => {
        flashTimers.delete(id)
        const next = new Set(flashIds.value)
        next.delete(id)
        flashIds.value = next
      }, FLASH_MS),
    )
  }

  /**
   * Fetch only this ticket and slot it in. Coalesced per ticket (a trailing refetch if an event arrives
   * mid-fetch). Results are discarded when a local mutation started after the fetch, or while a move
   * is pending. A 404 removes the card (the ticket is gone; this is not lost access). Resolves true
   * when the ticket is present afterwards.
   */
  function refreshTicket(id: string): Promise<boolean> {
    const cur = inflightRefresh.get(id)
    if (cur) {
      cur.again = true
      return cur.promise
    }
    const entry: { promise: Promise<boolean>; again: boolean } = { promise: undefined as unknown as Promise<boolean>, again: false }
    entry.promise = (async () => {
      try {
        for (let round = 0; round < 3; round++) {
          entry.again = false
          const started = mutationOf(id)
          let t: Ticket
          try {
            t = await ticketsApi.get(id)
          } catch (e) {
            if (e instanceof ApiError && e.status === 404) {
              removeLocal(id)
              return false
            }
            return !!ticketsById.value[id]
          }
          if (pendingMoves.value.has(id)) {
            refreshAfterSettle.add(id)
            return !!ticketsById.value[id]
          }
          if (mutationOf(id) !== started) {
            entry.again = true // discard and refetch once the mutation settled
          } else {
            upsert(t)
          }
          if (!entry.again) break
        }
        return !!ticketsById.value[id]
      } finally {
        inflightRefresh.delete(id)
      }
    })()
    inflightRefresh.set(id, entry)
    return entry.promise
  }

  function closePanelFor(t: TicketSummary): void {
    if (selectedRef.value !== t.ref) return
    const router = getRouter()
    const route = router?.currentRoute.value
    if (!router || !route) return
    void router.push({ name: 'board', params: { key: route.params.key }, query: route.query })
  }

  function shouldDefer(e: ApiEvent): boolean {
    if (!busy()) return false
    const id = e.ticket_id
    if (!id || !(e.type.startsWith('ticket.') || e.type.startsWith('comment.'))) return false
    if (pendingMoves.value.has(id) || e.type === 'ticket.created') return true
    const t = ticketsById.value[id]
    if (!t) return false
    const affected = new Set<Status>()
    if (dragging.value?.column) affected.add(dragging.value.column)
    for (const j of queue) {
      affected.add(j.fromStatus)
      affected.add(j.toStatus)
    }
    return affected.has(t.status)
  }

  /** Apply one stream event (see the event table in the Phase 5 spec, section 8). */
  async function applyEvent(e: ApiEvent): Promise<void> {
    if (project.value && e.project_id !== project.value.id) return
    if (shouldDefer(e)) {
      deferredEvents.value = [...deferredEvents.value, e]
      return
    }
    const mine = isMine(e)
    const id = e.ticket_id
    switch (e.type) {
      case 'ticket.created':
      case 'ticket.updated':
      case 'ticket.moved': {
        if (!ticketsLoaded.value || !id) return
        if (e.type === 'ticket.moved' && e.renumbered) {
          await reload({ ...ALL_PARTS, tickets: true })
        } else {
          await refreshTicket(id)
        }
        if (!mine && ticketsById.value[id]) flash(id)
        return
      }
      case 'ticket.deleted': {
        if (!id) return
        const t = ticketsById.value[id]
        removeLocal(id)
        if (t && !mine && selectedRef.value === t.ref) {
          closePanelFor(t)
          notify('info', `${t.ref} was deleted`)
        }
        return
      }
      case 'comment.added':
      case 'comment.changed': {
        if (!id) return
        commentVersions.value = { ...commentVersions.value, [id]: (commentVersions.value[id] ?? 0) + 1 }
        if (!ticketsLoaded.value) return
        await refreshTicket(id)
        if (!mine && ticketsById.value[id]) flash(id)
        return
      }
      case 'label.changed':
        await reload({ labels: true, tickets: ticketsLoaded.value })
        return
      case 'member.changed':
        await reload({ members: true, project: true, tickets: ticketsLoaded.value })
        return
      case 'project.updated':
        await reload({ project: true })
        void useProjectsStore().fetch()
        return
    }
  }

  function reset(): void {
    loadSeq++
    loadAC?.abort()
    loadAC = null
    loadPromise = null
    rerun = null
    deferredParts = null
    currentKey = null
    everLoaded = false
    for (const j of queue.splice(0)) j.resolve(false)
    pumping = false
    temps.clear()
    mutations.clear()
    inflightRefresh.clear()
    refreshAfterSettle.clear()
    for (const t of flashTimers.values()) clearTimeout(t)
    flashTimers.clear()
    project.value = null
    members.value = []
    labels.value = []
    ticketsById.value = {}
    columns.value = emptyColumns()
    columnKeys.value = emptyKeys()
    loadState.value = 'idle'
    error.value = null
    flashIds.value = new Set()
    pendingMoves.value = new Set()
    dragging.value = null
    deferredEvents.value = []
    leaving.value = false
    selectedRef.value = null
    lostAccess.value = false
    live.value = 'idle'
    ticketsLoaded.value = false
    fullById.value = {}
    commentVersions.value = {}
  }

  return {
    project, members, labels, ticketsById, columns, columnKeys, loadState, error, flashIds, pendingMoves, dragging,
    deferredEvents, leaving, selectedRef, lostAccess, live, ticketsLoaded, filters, fullById, commentVersions,
    role, isArchived, isOwner, isViewer, canEdit, counts, filtersActive, filteredColumns, filteredCounts,
    ticketByNumber, setFilters,
    load, reload, refetchProject, handleLostAccess, acknowledgeLostAccess,
    createTicket, updateTicket, deleteTicket, moveTicket, startDrag, endDrag,
    refreshTicket, applyEvent, reset, adopt: upsert,
  }
})
