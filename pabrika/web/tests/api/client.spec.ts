import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, configureClient, listAll, rateLimitMessage, request, resetUnauthorizedLatch } from '@/api/client'

type Resp = { status?: number; body?: unknown; raw?: string; headers?: Record<string, string> }

function mockFetch(...responses: (Resp | Error)[]) {
  const calls: { url: string; init: RequestInit }[] = []
  let i = 0
  const fn = vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const r = responses[Math.min(i++, responses.length - 1)]
    if (r instanceof Error) throw r
    const status = r.status ?? 200
    const text = r.raw ?? (r.body === undefined ? '' : JSON.stringify(r.body))
    return new Response(status === 204 ? null : text, { status, headers: r.headers })
  })
  return { fn, calls }
}

const err = (status: number, code: string, extra: object = {}, headers?: Record<string, string>): Resp => ({
  status,
  body: { error: { code, message: `msg ${code}`, ...extra } },
  headers,
})

const onUnauthorized = vi.fn()
const onToast = vi.fn()
const onForbidden = vi.fn()
const sleep = vi.fn(async () => {})

beforeEach(() => {
  onUnauthorized.mockReset()
  onToast.mockReset()
  onForbidden.mockReset()
  sleep.mockClear()
  resetUnauthorizedLatch()
  configureClient({ onUnauthorized, onToast, onForbidden, sleep })
})
afterEach(() => vi.unstubAllGlobals())

describe('request basics', () => {
  it('prefixes /api/v1, sets headers, parses JSON', async () => {
    const m = mockFetch({ body: { a: 1 } })
    vi.stubGlobal('fetch', m.fn)
    const res = await request<{ a: number }>('GET', '/projects', { query: { archived: true, x: undefined } })
    expect(res).toEqual({ a: 1 })
    expect(m.calls[0].url).toBe('/api/v1/projects?archived=true')
    const h = new Headers(m.calls[0].init.headers)
    expect(h.get('Accept')).toBe('application/json')
    expect(h.get('Content-Type')).toBeNull()
    expect(m.calls[0].init.credentials).toBe('same-origin')
  })
  it('sends Content-Type only with a body', async () => {
    const m = mockFetch({ body: {} })
    vi.stubGlobal('fetch', m.fn)
    await request('POST', '/projects', { body: { key: 'WEB' } })
    const h = new Headers(m.calls[0].init.headers)
    expect(h.get('Content-Type')).toBe('application/json')
    expect(m.calls[0].init.body).toBe('{"key":"WEB"}')
  })
  it('handles 204', async () => {
    vi.stubGlobal('fetch', mockFetch({ status: 204 }).fn)
    expect(await request('POST', '/auth/logout')).toBeUndefined()
  })
})

describe('ApiError parsing', () => {
  it('parses code, message and fields', async () => {
    vi.stubGlobal('fetch', mockFetch(err(422, 'validation_failed', { fields: { title: 'required' } })).fn)
    const e = await request('POST', '/projects/WEB/tickets', { body: {} }).catch((x) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 422, code: 'validation_failed', fields: { title: 'required' } })
  })
  it('parses Retry-After on 429', async () => {
    vi.stubGlobal('fetch', mockFetch(err(429, 'rate_limited', {}, { 'Retry-After': '17' })).fn)
    const e = await request('POST', '/auth/login', { body: {} }).catch((x) => x)
    expect(e.retryAfter).toBe(17)
    expect(rateLimitMessage(e)).toBe('Too many attempts, try again in 17 seconds')
    expect(rateLimitMessage(new ApiError(429, 'rate_limited', 'x'))).toBe('Too many attempts, try again in a minute')
  })
  it('network failure yields status 0 code network', async () => {
    vi.stubGlobal('fetch', mockFetch(new TypeError('failed')).fn)
    const e = await request('POST', '/projects', { body: {} }).catch((x) => x)
    expect(e).toMatchObject({ status: 0, code: 'network' })
  })
  it('non-JSON error body yields status 0 code network', async () => {
    vi.stubGlobal('fetch', mockFetch({ status: 502, raw: '<html>bad gateway</html>' }).fn)
    const e = await request('POST', '/projects', { body: {} }).catch((x) => x)
    expect(e).toMatchObject({ status: 0, code: 'network' })
  })
})

describe('401 handling', () => {
  it('calls onUnauthorized exactly once for parallel failures', async () => {
    vi.stubGlobal('fetch', mockFetch(err(401, 'unauthorized')).fn)
    await Promise.allSettled([request('GET', '/projects'), request('GET', '/tokens'), request('POST', '/projects', { body: {} })])
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })
  it.each([
    ['POST', '/auth/login'],
    ['POST', '/auth/signup'],
  ])('does not redirect on %s %s', async (method, path) => {
    vi.stubGlobal('fetch', mockFetch(err(401, 'unauthorized')).fn)
    await request(method, path, { body: {} }).catch(() => {})
    expect(onUnauthorized).not.toHaveBeenCalled()
  })
  it('does not redirect for the initial /auth/me (skipUnauthorized)', async () => {
    vi.stubGlobal('fetch', mockFetch(err(401, 'unauthorized')).fn)
    await request('GET', '/auth/me', { skipUnauthorized: true }).catch(() => {})
    expect(onUnauthorized).not.toHaveBeenCalled()
  })
  it('invalid_credentials never redirects', async () => {
    vi.stubGlobal('fetch', mockFetch(err(401, 'invalid_credentials')).fn)
    await request('GET', '/projects').catch(() => {})
    expect(onUnauthorized).not.toHaveBeenCalled()
  })
  it('can fire again after the latch is reset', async () => {
    vi.stubGlobal('fetch', mockFetch(err(401, 'unauthorized')).fn)
    await request('GET', '/projects').catch(() => {})
    resetUnauthorizedLatch()
    await request('GET', '/projects').catch(() => {})
    expect(onUnauthorized).toHaveBeenCalledTimes(2)
  })
})

describe('403 handling', () => {
  it('origin_mismatch toasts the BASE_URL hint', async () => {
    vi.stubGlobal('fetch', mockFetch(err(403, 'origin_mismatch')).fn)
    await request('POST', '/projects', { body: {} }).catch(() => {})
    expect(onToast).toHaveBeenCalledWith('error', expect.stringContaining('BASE_URL'))
  })
  it('forbidden toasts the server message and notifies onForbidden', async () => {
    vi.stubGlobal('fetch', mockFetch(err(403, 'forbidden')).fn)
    await request('PATCH', '/tickets/x', { body: {} }).catch(() => {})
    expect(onToast).toHaveBeenCalledWith('error', 'msg forbidden')
    expect(onForbidden).toHaveBeenCalledTimes(1)
  })
})

describe('retry and toasts', () => {
  it('retries reads on 503 with capped backoff then succeeds', async () => {
    const m = mockFetch(err(503, 'unavailable'), err(503, 'unavailable'), { body: { ok: true } })
    vi.stubGlobal('fetch', m.fn)
    expect(await request('GET', '/projects')).toEqual({ ok: true })
    expect(m.fn).toHaveBeenCalledTimes(3)
    expect(sleep).toHaveBeenCalledTimes(2)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })
  it('retries reads on network errors and gives up after the cap', async () => {
    const m = mockFetch(new TypeError('x'))
    vi.stubGlobal('fetch', m.fn)
    const e = await request('GET', '/projects').catch((x) => x)
    expect(e.code).toBe('network')
    expect(m.fn.mock.calls.length).toBeGreaterThan(1)
    expect(m.fn.mock.calls.length).toBeLessThanOrEqual(5)
    const delays = sleep.mock.calls.map((c) => (c as unknown as [number])[0])
    expect(Math.max(...delays)).toBeLessThanOrEqual(8000)
  })
  it('honors Retry-After on a 429 read', async () => {
    const m = mockFetch(err(429, 'rate_limited', {}, { 'Retry-After': '3' }), { body: { ok: 1 } })
    vi.stubGlobal('fetch', m.fn)
    await request('GET', '/projects')
    expect(sleep).toHaveBeenCalledWith(3000)
  })
  it('never retries mutations', async () => {
    const m = mockFetch(err(503, 'unavailable'))
    vi.stubGlobal('fetch', m.fn)
    await request('POST', '/projects', { body: {} }).catch(() => {})
    expect(m.fn).toHaveBeenCalledTimes(1)
  })
  it('toasts on mutation 5xx and network errors', async () => {
    vi.stubGlobal('fetch', mockFetch(err(500, 'internal')).fn)
    await request('POST', '/projects', { body: {} }).catch(() => {})
    expect(onToast).toHaveBeenLastCalledWith('error', 'Something went wrong')
    vi.stubGlobal('fetch', mockFetch(new TypeError('x')).fn)
    await request('POST', '/projects', { body: {} }).catch(() => {})
    expect(onToast).toHaveBeenLastCalledWith('error', 'Network error, check your connection')
  })
  it('does not retry or toast when the request is aborted', async () => {
    const ac = new AbortController()
    const m = mockFetch(Object.assign(new Error('aborted'), { name: 'AbortError' }))
    vi.stubGlobal('fetch', m.fn)
    ac.abort()
    const e = await request('GET', '/projects', { signal: ac.signal }).catch((x) => x)
    expect(e.name).toBe('AbortError')
    expect(m.fn).toHaveBeenCalledTimes(1)
    expect(onToast).not.toHaveBeenCalled()
  })
})

describe('listAll', () => {
  it('follows next_cursor until null with limit 200', async () => {
    const m = mockFetch(
      { body: { items: [1, 2], next_cursor: 'abc' } },
      { body: { items: [3], next_cursor: null } },
    )
    vi.stubGlobal('fetch', m.fn)
    const items = await listAll<number>('/projects/WEB/tickets')
    expect(items).toEqual([1, 2, 3])
    expect(m.calls[0].url).toBe('/api/v1/projects/WEB/tickets?limit=200')
    expect(m.calls[1].url).toBe('/api/v1/projects/WEB/tickets?limit=200&cursor=abc')
  })
  it('aborts with an error when a cursor is invalid', async () => {
    vi.stubGlobal('fetch', mockFetch({ body: { items: [1], next_cursor: 'x' } }, err(400, 'invalid_cursor')).fn)
    await expect(listAll('/projects/WEB/tickets')).rejects.toMatchObject({ code: 'invalid_cursor' })
  })
  it('passes the signal through', async () => {
    const m = mockFetch({ body: { items: [], next_cursor: null } })
    vi.stubGlobal('fetch', m.fn)
    const ac = new AbortController()
    await listAll('/x', {}, ac.signal)
    expect(m.calls[0].init.signal).toBe(ac.signal)
  })
})
