import { ref, watch, type Ref } from 'vue'
import { ApiError } from '@/api/client'
import { comments as commentsApi } from '@/api/comments'
import { tickets as ticketsApi } from '@/api/tickets'
import type { Activity, Comment, Ticket } from '@/api/types'
import { useBoardStore } from '@/stores/board'

export type PartState = 'idle' | 'loading' | 'ready' | 'error' | 'not-found'

const isAbort = (e: unknown): boolean => typeof e === 'object' && e !== null && (e as { name?: string }).name === 'AbortError'

/**
 * Panel data for one ticket: the full ticket (with description), its comments and the first page of
 * activity. `target` is a ULID or a `KEY-n` reference. Each part loads in parallel and fails on its own.
 * Follows the board store: a refreshed full ticket replaces `ticket`, and comment events (or any ticket
 * change) refetch comments and activity.
 */
export function useTicketDetail(target: Ref<string | null>) {
  const board = useBoardStore()

  const ticket = ref<Ticket | null>(null)
  const ticketState = ref<PartState>('idle')
  const comments = ref<Comment[]>([])
  const commentsState = ref<PartState>('idle')
  const activity = ref<Activity[]>([])
  const activityNext = ref<string | null>(null)
  const activityState = ref<PartState>('idle')
  const activityMore = ref(false)

  let seq = 0
  let ac: AbortController | null = null

  async function loadTicket(t: string, signal: AbortSignal, mySeq: number): Promise<void> {
    ticketState.value = 'loading'
    try {
      const res = await ticketsApi.get(t, signal)
      if (mySeq !== seq) return
      ticket.value = res
      ticketState.value = 'ready'
    } catch (e) {
      if (mySeq !== seq || isAbort(e)) return
      ticketState.value = e instanceof ApiError && e.status === 404 ? 'not-found' : 'error'
    }
  }

  async function loadComments(id: string, signal: AbortSignal, mySeq: number, quiet = false): Promise<void> {
    if (!quiet) commentsState.value = 'loading'
    try {
      const list = await commentsApi.listAll(id, signal)
      if (mySeq !== seq) return
      comments.value = list
      commentsState.value = 'ready'
    } catch (e) {
      if (mySeq !== seq || isAbort(e)) return
      if (!quiet) commentsState.value = 'error'
    }
  }

  async function loadActivity(id: string, signal: AbortSignal, mySeq: number, quiet = false): Promise<void> {
    if (!quiet) activityState.value = 'loading'
    try {
      const page = await commentsApi.activity(id, null, signal)
      if (mySeq !== seq) return
      activity.value = page.items
      activityNext.value = page.next_cursor
      activityState.value = 'ready'
    } catch (e) {
      if (mySeq !== seq || isAbort(e)) return
      if (!quiet) activityState.value = 'error'
    }
  }

  /** Comments and activity need the ULID; it is known once the ticket loaded (or immediately when given). */
  function ticketId(): string | null {
    return ticket.value?.id ?? (target.value && !/^[A-Za-z]+-\d+$/.test(target.value) ? target.value : null)
  }

  function reset(): void {
    ac?.abort()
    seq++
    ticket.value = null
    ticketState.value = 'idle'
    comments.value = []
    commentsState.value = 'idle'
    activity.value = []
    activityNext.value = null
    activityState.value = 'idle'
  }

  function start(t: string): void {
    reset()
    const mySeq = seq
    ac = new AbortController()
    const signal = ac.signal
    const known = !/^[A-Za-z]+-\d+$/.test(t)
    if (known) {
      // The ULID is known (the ticket is on the board): everything loads in parallel.
      void loadTicket(t, signal, mySeq)
      void loadComments(t, signal, mySeq)
      void loadActivity(t, signal, mySeq)
    } else {
      // A bare reference (cold deep link): resolve it first, then load the rest by id.
      void loadTicket(t, signal, mySeq).then(() => {
        if (mySeq !== seq || !ticket.value) return
        void loadComments(ticket.value.id, signal, mySeq)
        void loadActivity(ticket.value.id, signal, mySeq)
      })
    }
  }

  watch(
    target,
    (t) => {
      if (t) start(t)
      else reset()
    },
    { immediate: true },
  )

  const retryTicket = () => target.value && start(target.value)
  function retryComments(): void {
    const id = ticketId()
    if (id && ac) void loadComments(id, ac.signal, seq)
  }
  function retryActivity(): void {
    const id = ticketId()
    if (id && ac) void loadActivity(id, ac.signal, seq)
  }

  async function loadMoreActivity(): Promise<void> {
    const id = ticketId()
    if (!id || !activityNext.value || activityMore.value || !ac) return
    const mySeq = seq
    activityMore.value = true
    try {
      const page = await commentsApi.activity(id, activityNext.value, ac.signal)
      if (mySeq !== seq) return
      activity.value = [...activity.value, ...page.items]
      activityNext.value = page.next_cursor
    } catch (e) {
      if (!isAbort(e)) activityState.value = 'error'
    } finally {
      activityMore.value = false
    }
  }

  function upsertComment(c: Comment): void {
    const i = comments.value.findIndex((x) => x.id === c.id)
    if (i >= 0) comments.value = comments.value.map((x) => (x.id === c.id ? c : x))
    else comments.value = [...comments.value, c]
  }
  function removeComment(id: string): void {
    comments.value = comments.value.filter((c) => c.id !== id)
  }

  // A refreshed full ticket (event, own edit or move) replaces the panel copy and refreshes the activity.
  watch(
    () => {
      const id = ticket.value?.id
      return id ? board.fullById[id] : undefined
    },
    (t) => {
      if (!t || !ticket.value || t.id !== ticket.value.id || t === ticket.value) return
      ticket.value = t
      if (ac) void loadActivity(t.id, ac.signal, seq, true)
    },
  )

  // Comment events name this ticket: refetch comments and activity.
  watch(
    () => {
      const id = ticketId()
      return id ? (board.commentVersions[id] ?? 0) : 0
    },
    (v, old) => {
      const id = ticketId()
      if (!id || !ac || v === old) return
      void loadComments(id, ac.signal, seq, true)
      void loadActivity(id, ac.signal, seq, true)
    },
  )

  return {
    ticket, ticketState, comments, commentsState, activity, activityNext, activityState, activityMore,
    retryTicket, retryComments, retryActivity, loadMoreActivity, upsertComment, removeComment,
  }
}
