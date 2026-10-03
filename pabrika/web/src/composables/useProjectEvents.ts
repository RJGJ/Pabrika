import { onScopeDispose, watch } from 'vue'
import { ApiError } from '@/api/client'
import { ES_CLOSED, openProjectStream, type ProjectStream } from '@/api/events'
import { projects as projectsApi } from '@/api/projects'
import type { ApiEvent } from '@/api/types'
import { useBoardStore } from '@/stores/board'

const BACKOFF_BASE_MS = 3000
const BACKOFF_MAX_MS = 30000

/**
 * What the stream drives. The default targets the board store; the settings screen passes its own
 * binding so both screens share the same reconnect, probe and backoff logic.
 */
export interface EventsBinding {
  /** True while the screen has loaded the project (the stream is open only then). */
  ready(): boolean
  /** True once access was lost (the stream stays closed). */
  lost(): boolean
  setLive(state: 'idle' | 'live' | 'reconnecting'): void
  onEvent(e: ApiEvent): void
  /** Called on every open (first included): reload quietly, there is no replay. */
  onOpen(key: string): void
  /** 404 on the probe. Must be a no-op while the user is leaving or deleting the project. */
  onLostAccess(): void
}

export interface EventsControl {
  /** Stop the stream (used before leave and delete). */
  close(): void
  /** Reopen it after a failed leave or delete, when the screen is still ready. */
  reopen(): void
}

/**
 * Keeps one EventSource open for the project while the board store is `ready`.
 *
 * - every `onopen` (first included) runs a quiet reload, because the stream has no replay;
 * - `onerror` with CONNECTING: the browser retries by itself, we only show "Reconnecting";
 * - `onerror` with CLOSED: close, probe `GET /projects/{key}`: 401 is handled by the client (login),
 *   404 is lost access, anything else recreates the stream with capped backoff (3, 6, 12 ... 30 s).
 * It closes on unmount, key change, and whenever the store leaves `ready` (logout, 401, lost access).
 */
export function useProjectEvents(
  getKey: () => string | null | undefined,
  binding?: EventsBinding,
): EventsControl {
  const board = useBoardStore()
  const b: EventsBinding = binding ?? {
    ready: () => board.loadState === 'ready',
    lost: () => board.lostAccess,
    setLive: (s) => {
      board.live = s
    },
    onEvent: (e) => void board.applyEvent(e),
    onOpen: (key) => void board.load(key, { quiet: true, tickets: board.ticketsLoaded }),
    onLostAccess: () => board.handleLostAccess(),
  }
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
    b.setLive('idle')
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
    b.setLive('reconnecting')
    let retryAfter: number | undefined
    try {
      await projectsApi.get(key)
    } catch (e) {
      if (gen !== generation) return
      if (e instanceof ApiError) {
        if (e.status === 401) return // the client already started the login redirect
        if (e.status === 404) {
          b.onLostAccess() // a no-op while `leaving`
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
        if (gen === generation) b.onEvent(e)
      },
      onOpen: () => {
        if (gen !== generation) return
        attempt = 0
        b.setLive('live')
        b.onOpen(key)
      },
      onError: (readyState) => {
        if (gen !== generation) return
        if (readyState === ES_CLOSED) void onClosed(key, gen)
        else b.setLive('reconnecting')
      },
    })
  }

  watch(
    [getKey, () => b.ready(), () => b.lost()] as const,
    ([key, ready, lost]) => {
      if (!key || !ready || lost) {
        if (activeKey !== null) stop()
        return
      }
      if (activeKey !== null && activeKey.toLowerCase() === key.toLowerCase()) return
      open(key)
    },
    { immediate: true },
  )

  onScopeDispose(stop)

  return {
    close: stop,
    reopen: () => {
      const key = getKey()
      if (key && b.ready() && !b.lost() && activeKey === null) open(key)
    },
  }
}
