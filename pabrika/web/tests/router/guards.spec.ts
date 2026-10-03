import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { ApiError } from '@/api/client'

vi.mock('@/api/auth', () => ({ auth: { config: vi.fn(), me: vi.fn() } }))

import { auth as api } from '@/api/auth'
import { createAppRouter, guardDecision } from '@/router'
import { useAuthStore } from '@/stores/auth'

const mocked = api as unknown as Record<string, ReturnType<typeof vi.fn>>
const user = { id: 'u1', email: 'a@x.co', display_name: 'Ada', created_at: '' }

function signedIn() {
  mocked.me.mockResolvedValue({ user, auth: { method: 'session' } })
}
function signedOut() {
  mocked.me.mockRejectedValue(new ApiError(401, 'unauthorized', 'no'))
}
function makeRouter() {
  return createAppRouter(createMemoryHistory())
}

beforeEach(() => {
  setActivePinia(createPinia())
  mocked.config.mockReset()
  mocked.me.mockReset()
  mocked.config.mockResolvedValue({ signup_enabled: true })
})

describe('guards', () => {
  it('redirects an anonymous visitor to /login with the redirect param', async () => {
    signedOut()
    const r = makeRouter()
    await r.push('/p/WEB/t/12?q=a')
    expect(r.currentRoute.value.name).toBe('login')
    expect(r.currentRoute.value.query.redirect).toBe('/p/WEB/t/12?q=a')
  })
  it('omits the redirect param for the home route', async () => {
    signedOut()
    const r = makeRouter()
    await r.push('/')
    expect(r.currentRoute.value.fullPath).toBe('/login')
  })
  it('sends a signed-in user away from /login and /signup', async () => {
    signedIn()
    const r = makeRouter()
    await r.push('/login')
    expect(r.currentRoute.value.name).toBe('home')
    await r.push('/signup')
    expect(r.currentRoute.value.name).toBe('home')
  })
  it('redirects /signup to /login when signup is disabled', async () => {
    mocked.config.mockResolvedValue({ signup_enabled: false })
    signedOut()
    const r = makeRouter()
    await r.push('/signup')
    expect(r.currentRoute.value.name).toBe('login')
    expect(useAuthStore().signupEnabled).toBe(false)
  })
  it('allows /signup when signup is enabled', async () => {
    signedOut()
    const r = makeRouter()
    await r.push('/signup')
    expect(r.currentRoute.value.name).toBe('signup')
  })
  it('keeps the URL (no login redirect) when /auth/me fails with a network error', async () => {
    mocked.me.mockRejectedValue(new ApiError(0, 'network', 'x'))
    const r = makeRouter()
    await r.push('/p/WEB')
    expect(useAuthStore().status).toBe('unreachable')
    expect(r.currentRoute.value.fullPath).toBe('/p/WEB')
  })
  it('keeps the URL on a 5xx from /auth/me', async () => {
    mocked.me.mockRejectedValue(new ApiError(500, 'internal', 'x'))
    const r = makeRouter()
    await r.push('/settings')
    expect(r.currentRoute.value.name).toBe('account')
  })
  it('bootstraps only once across navigations', async () => {
    signedIn()
    const r = makeRouter()
    await r.push('/settings')
    await r.push('/p/WEB')
    expect(mocked.me).toHaveBeenCalledTimes(1)
  })
  it('unknown paths render not-found for signed-in users', async () => {
    signedIn()
    const r = makeRouter()
    await r.push('/nope/nothing')
    expect(r.currentRoute.value.name).toBe('not-found')
  })
})

describe('route table', () => {
  it('serves the board and the ticket panel with one component', () => {
    const r = makeRouter()
    const a = r.resolve('/p/WEB')
    const b = r.resolve('/p/WEB/t/12')
    expect(a.matched[0].components?.default).toBe(b.matched[0].components?.default)
    expect(a.name).toBe('board')
    expect(b.name).toBe('ticket')
    expect(b.params).toMatchObject({ key: 'WEB', number: '12' })
  })
  it('has the settings routes', () => {
    const r = makeRouter()
    expect(r.resolve('/p/WEB/settings').name).toBe('project-settings')
    expect(r.resolve('/settings').name).toBe('account')
  })
})

describe('guardDecision', () => {
  const route = (name: string, pub = false, fullPath = '/x') => ({ name, fullPath, meta: { public: pub } })
  it('lets everything through when unreachable', () => {
    expect(guardDecision(route('board'), { status: 'unreachable', signupEnabled: true })).toBe(true)
  })
  it('redirects anon users on protected routes', () => {
    expect(guardDecision(route('board', false, '/p/WEB'), { status: 'anon', signupEnabled: true })).toEqual({
      name: 'login',
      query: { redirect: '/p/WEB' },
    })
  })
})
