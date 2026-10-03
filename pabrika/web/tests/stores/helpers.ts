import { vi } from 'vitest'
import type { ApiEvent, Label, Member, ProjectDetail, Ticket, TicketSummary } from '@/api/types'
import { ApiError } from '@/api/client'

export const ME = { id: 'u-me', email: 'me@x.io', display_name: 'Me', created_at: '' }

export const project = (over: Partial<ProjectDetail> = {}): ProjectDetail => ({
  id: 'p1', key: 'WEB', name: 'Web', description: '', archived_at: null, created_at: '', updated_at: '',
  role: 'owner', counts: { backlog: 0, todo: 0, in_progress: 0, done: 0 }, ...over,
})

export const ticket = (n: number, over: Partial<TicketSummary> = {}): TicketSummary => ({
  id: `t${n}`, ref: `WEB-${n}`, project_id: 'p1', project_key: 'WEB', number: n, title: `Ticket ${n}`,
  status: 'backlog', priority: 'medium', assignee: null, labels: [], position: n * 1024, due_date: null,
  comment_count: 0, created_at: '', updated_at: '', ...over,
})

export const full = (t: TicketSummary, description = ''): Ticket => ({ ...t, description })

export const member = (id: string, name = id): Member => ({
  user: { id, email: `${id}@x.io`, display_name: name }, role: 'editor', created_at: '',
})

export const label = (id: string, name = id): Label => ({ id, project_id: 'p1', name, color: 'red' })

export const event = (type: ApiEvent['type'], over: Partial<ApiEvent> = {}): ApiEvent => ({
  type, project_id: 'p1', actor: { type: 'user', id: 'someone-else' }, at: '', ...over,
})

export const apiError = (status: number, code = 'x', message = 'nope') => new ApiError(status, code, message)

/** A promise whose resolution the test controls. */
export function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

export const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve()
}

export type Mocked = Record<string, ReturnType<typeof vi.fn>>
