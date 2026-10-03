import { EVENT_TYPES, type ApiEvent, type EventType } from './types'

export interface EventSourceLike {
  readonly readyState: number
  onopen: ((ev: Event) => unknown) | null
  onerror: ((ev: Event) => unknown) | null
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void
  close(): void
}
export type EventSourceFactory = (url: string) => EventSourceLike

export const ES_CONNECTING = 0
export const ES_OPEN = 1
export const ES_CLOSED = 2

let factory: EventSourceFactory = (url) => new EventSource(url) as unknown as EventSourceLike

/** Tests swap in a FakeEventSource. */
export function setEventSourceFactory(f: EventSourceFactory): void {
  factory = f
}

export function eventsUrl(projectKey: string): string {
  return `/api/v1/projects/${encodeURIComponent(projectKey)}/events`
}

export interface ProjectStream {
  source: EventSourceLike
  close(): void
}

/**
 * Opens the project stream with one named-event listener per type (no onmessage).
 * Malformed frames are ignored.
 */
export function openProjectStream(
  projectKey: string,
  handlers: {
    onEvent: (e: ApiEvent) => void
    onOpen?: () => void
    onError?: (readyState: number) => void
  },
  kinds: readonly EventType[] = EVENT_TYPES,
): ProjectStream {
  const source = factory(eventsUrl(projectKey))
  source.onopen = () => handlers.onOpen?.()
  source.onerror = () => handlers.onError?.(source.readyState)
  for (const kind of kinds) {
    source.addEventListener(kind, (ev) => {
      try {
        const data = JSON.parse(ev.data as string) as ApiEvent
        handlers.onEvent({ ...data, type: kind })
      } catch {
        /* ignore malformed frame */
      }
    })
  }
  return { source, close: () => source.close() }
}
