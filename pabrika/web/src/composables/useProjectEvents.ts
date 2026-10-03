import { onScopeDispose, watch } from 'vue'
import { ApiError } from '@/api/client'
import { ES_CLOSED, openProjectStream, type ProjectStream } from '@/api/events'
import { projects as projectsApi } from '@/api/projects'
import { useBoardStore } from '@/stores/board'

const BACKOFF_BASE_MS = 3000
const BACKOFF_MAX_MS = 30000

/**
 * Keeps one EventSource open for the project while the board store is `ready`.
 *
 * - every `onopen` (first included) runs a quiet reload, because the stream has no replay;
 * - `onerror` with CONNECTING: the browser retries by itself, we only show "Reconnecting";
 * - `onerror` with CLOSED: close, probe `GET /projects/{key}`: 401 is handled by the client (login),
 *   404 is lost access, anything else recreates the stream with capped backoff (3, 6, 12 ... 30 s).
 * It closes on unmount, key change, and whenever the store leaves `ready` (logout, 401, lost access).
 */
export function useProjectEvents(getKey: () => string | null | undefined) {
  const board = useBoardStore()
  let stream: ProjectStream | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let activeKey: string | null = null
  let generation = 0
  let attempt = 0

  function closeStream(): void {
    clearTimeout(timer)
    timer = undefined
    stream?.close()
    stream = null
  }

  function stop(): void {
    generation++
    closeStream()
    activeKey = null
    attempt = 0
    board.live = 'idle'
  }

  function schedule(key: string, gen: number, retryAfterSec?: number): void {
    let delay = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** attempt)
    attempt++
    if (retryAfterSec) delay = Math.max(delay, retryAfterSec * 1000)
    timer = setTimeout(() => {
      timer = undefined
      if (gen === generation) open(key)
    }, delay)
  }

  async function onClosed(key: string, gen: number): Promise<void> {
    closeStream()
    board.live = 'reconnecting'
    let retryAfter: number | undefined
    try {
      await projectsApi.get(key)
    } catch (e) {
      if (gen !== generation) return
      if (e instanceof ApiError) {
        if (e.status === 401) return // the client already started the login redirect
        if (e.status === 404) {
          board.handleLostAccess() // a no-op while `leaving`
          return
        }
        retryAfter = e.retryAfter
      }
    }
    if (gen !== generation) return
    schedule(key, gen, retryAfter)
  }

  function open(key: string): void {
    closeStream()
    activeKey = key
    const gen = ++generation
    stream = openProjectStream(key, {
      onEvent: (e) => {
        if (gen === generation) void board.applyEvent(e)
      },
      onOpen: () => {
        if (gen !== generation) return
        attempt = 0
        board.live = 'live'
        void board.load(key, { quiet: true, tickets: board.ticketsLoaded })
      },
      onError: (readyState) => {
        if (gen !== generation) return
        if (readyState === ES_CLOSED) void onClosed(key, gen)
        else board.live = 'reconnecting'
      },
    })
  }

  watch(
    [getKey, () => board.loadState, () => board.lostAccess] as const,
    ([key, state, lost]) => {
      if (!key || state !== 'ready' || lost) {
        if (activeKey !== null) stop()
        return
      }
      if (activeKey !== null && activeKey.toLowerCase() === key.toLowerCase()) return
      open(key)
    },
    { immediate: true },
  )

  onScopeDispose(stop)
}
