import type { Envelope, ErrorBody } from './types'

export const API_BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  code: string
  fields?: Record<string, string>
  retryAfter?: number

  constructor(status: number, code: string, message: string, fields?: Record<string, string>, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.retryAfter = retryAfter
  }
}

export type ToastKind = 'error' | 'success' | 'info'

interface ClientConfig {
  onUnauthorized: () => void
  onToast: (kind: ToastKind, message: string) => void
  /** Called after a 403 forbidden / insufficient_scope (the role may have changed). */
  onForbidden: () => void
  sleep: (ms: number) => Promise<void>
}

const config: ClientConfig = {
  onUnauthorized: () => {},
  onToast: () => {},
  onForbidden: () => {},
  sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
}

/** Inject callbacks so the client has no store or router imports (no cycles). */
export function configureClient(partial: Partial<ClientConfig>): void {
  Object.assign(config, partial)
}

let unauthorizedFired = false
/** Re-arm the one-shot 401 redirect (after a successful login, signup or bootstrap). */
export function resetUnauthorizedLatch(): void {
  unauthorizedFired = false
}

export interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number | boolean | undefined | null>
  signal?: AbortSignal
  /** Do not run the global 401 handling (initial GET /auth/me). */
  skipUnauthorized?: boolean
  /** Do not show a toast for 5xx/network failures. */
  silent?: boolean
}

const MAX_READ_RETRIES = 3
const BACKOFF_MS = [500, 1000, 2000, 4000]

function buildUrl(path: string, query?: RequestOptions['query']): string {
  let url = API_BASE + path
  if (query) {
    const params = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) {
      if (v !== undefined && v !== null) params.set(k, String(v))
    }
    const qs = params.toString()
    if (qs) url += '?' + qs
  }
  return url
}

export function rateLimitMessage(e: { retryAfter?: number }): string {
  return e.retryAfter
    ? `Too many attempts, try again in ${e.retryAfter} seconds`
    : 'Too many attempts, try again in a minute'
}

function isAbort(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { name?: string }).name === 'AbortError'
}

async function attempt<T>(method: string, path: string, opts: RequestOptions): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = { method, headers, credentials: 'same-origin', signal: opts.signal }
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(opts.body)
  }
  let res: Response
  try {
    res = await fetch(buildUrl(path, opts.query), init)
  } catch (e) {
    if (isAbort(e)) throw e
    throw new ApiError(0, 'network', 'Network error')
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (res.ok) {
    if (text === '') return undefined as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new ApiError(0, 'network', 'Invalid response from server')
    }
  }
  let parsed: ErrorBody | undefined
  try {
    parsed = JSON.parse(text) as ErrorBody
  } catch {
    parsed = undefined
  }
  if (!parsed || typeof parsed !== 'object' || !parsed.error || typeof parsed.error.code !== 'string') {
    throw new ApiError(0, 'network', 'Unexpected response from server')
  }
  const ra = res.headers.get('Retry-After')
  const retryAfter = res.status === 429 && ra && /^\d+$/.test(ra) ? Number(ra) : undefined
  throw new ApiError(res.status, parsed.error.code, parsed.error.message, parsed.error.fields, retryAfter)
}

function isTransient(e: ApiError): boolean {
  return e.code === 'network' || e.status === 503 || e.status === 429
}

export async function request<T = void>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const isRead = method === 'GET'
  let tries = 0
  for (;;) {
    try {
      return await attempt<T>(method, path, opts)
    } catch (e) {
      if (!(e instanceof ApiError)) throw e // aborts pass through untouched
      if (isRead && isTransient(e) && tries < MAX_READ_RETRIES && !opts.signal?.aborted) {
        const wait = e.status === 429 && e.retryAfter ? e.retryAfter * 1000 : BACKOFF_MS[Math.min(tries, BACKOFF_MS.length - 1)]
        tries++
        await config.sleep(wait)
        continue
      }
      handleError(e, method, path, opts)
      throw e
    }
  }
}

function handleError(e: ApiError, method: string, path: string, opts: RequestOptions): void {
  const authEndpoint = (method === 'POST' && (path === '/auth/login' || path === '/auth/signup')) || opts.skipUnauthorized
  if (e.status === 401 && e.code === 'unauthorized' && !authEndpoint) {
    if (!unauthorizedFired) {
      unauthorizedFired = true
      config.onUnauthorized()
    }
    return
  }
  if (e.status === 403) {
    if (e.code === 'origin_mismatch') {
      config.onToast('error', "Request blocked: the page origin does not match the server's BASE_URL")
    } else if (e.code === 'forbidden' || e.code === 'insufficient_scope') {
      config.onToast('error', e.message)
      config.onForbidden()
    }
    return
  }
  if (opts.silent || method === 'GET') return // reads surface inline errors
  if (e.code === 'network') config.onToast('error', 'Network error, check your connection')
  else if (e.status >= 500) config.onToast('error', 'Something went wrong')
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, opts),
  post: <T = void>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>('POST', path, { ...opts, body: body === undefined ? {} : body }),
  patch: <T>(path: string, body: unknown, opts?: RequestOptions) => request<T>('PATCH', path, { ...opts, body }),
  delete: (path: string, opts?: RequestOptions) => request<void>('DELETE', path, opts),
}

/** Follow next_cursor until null (limit 200 per page). */
export async function listAll<T>(
  path: string,
  query: Record<string, string | number | boolean | undefined> = {},
  signal?: AbortSignal,
): Promise<T[]> {
  const out: T[] = []
  let cursor: string | null = null
  do {
    const page: Envelope<T> = await request<Envelope<T>>('GET', path, {
      query: { limit: 200, ...query, cursor: cursor ?? undefined },
      signal,
    })
    out.push(...page.items)
    cursor = page.next_cursor
  } while (cursor)
  return out
}
