import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory } from 'vue-router'
import { ApiError, configureClient, resetUnauthorizedLatch } from '@/api/client'

vi.mock('@/api/auth', () => ({
  auth: {
    config: vi.fn(), me: vi.fn(), login: vi.fn(), signup: vi.fn(), logout: vi.fn(),
    updateProfile: vi.fn(), changePassword: vi.fn(),
  },
}))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { auth as api } from '@/api/auth'
import { notify } from '@/lib/toast'
import { createAppRouter } from '@/router'
import { setRouter } from '@/router/instance'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'

const mocked = api as unknown as Record<string, ReturnType<typeof vi.fn>>
const authUser = { id: 'u1', email: 'a@x.co', display_name: 'Ada', created_at: '2026-01-01T00:00:00Z' }

beforeEach(() => {
  setActivePinia(createPinia())
  for (const fn of Object.values(mocked)) fn.mockReset()
  mocked.config.mockResolvedValue({ signup_enabled: true })
  vi.mocked(notify).mockReset()
  resetUnauthorizedLatch()
})

describe('bootstrap', () => {
  it('authed when /auth/me succeeds', async () => {
    mocked.me.mockResolvedValue({ user: authUser, auth: { method: 'session' } })
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.status).toBe('authed')
    expect(s.user?.display_name).toBe('Ada')
  })
  it('anon when /auth/me is 401', async () => {
    mocked.me.mockRejectedValue(new ApiError(401, 'unauthorized', 'no'))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.status).toBe('anon')
    expect(s.user).toBeNull()
  })
  it.each([
    ['network', new ApiError(0, 'network', 'x')],
    ['5xx', new ApiError(500, 'internal', 'x')],
    ['503', new ApiError(503, 'unavailable', 'x')],
  ])('unreachable (not anon) on %s failure', async (_n, err) => {
    mocked.me.mockRejectedValue(err)
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.status).toBe('unreachable')
  })
  it('reads signup_enabled from the config', async () => {
    mocked.config.mockResolvedValue({ signup_enabled: false })
    mocked.me.mockRejectedValue(new ApiError(401, 'unauthorized', 'no'))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.signupEnabled).toBe(false)
  })
  it('assumes signup is enabled when the config call fails', async () => {
    mocked.config.mockRejectedValue(new ApiError(500, 'internal', 'x'))
    mocked.me.mockRejectedValue(new ApiError(401, 'unauthorized', 'no'))
    const s = useAuthStore()
    await s.bootstrap()
    expect(s.signupEnabled).toBe(true)
  })
  it('coalesces concurrent calls', async () => {
    mocked.me.mockResolvedValue({ user: authUser, auth: { method: 'session' } })
    const s = useAuthStore()
    await Promise.all([s.bootstrap(), s.bootstrap()])
    expect(mocked.me).toHaveBeenCalledTimes(1)
  })
})

describe('actions', () => {
  it('login and signup set the user', async () => {
    mocked.login.mockResolvedValue({ user: authUser })
    mocked.signup.mockResolvedValue({ user: authUser })
    const s = useAuthStore()
    await s.login('a@x.co', 'pw')
    expect(s.status).toBe('authed')
    s.user = null
    await s.signup('a@x.co', 'Ada', 'long-enough-pw')
    expect((s.user as { id: string } | null)?.id).toBe('u1')
  })
  it('a failed login leaves the store anonymous and rethrows', async () => {
    mocked.login.mockRejectedValue(new ApiError(401, 'invalid_credentials', 'Invalid email or password'))
    const s = useAuthStore()
    s.status = 'anon'
    await expect(s.login('a', 'b')).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(s.status).toBe('anon')
  })
  it('signup 404 refreshes the config (signup disabled meanwhile)', async () => {
    mocked.signup.mockRejectedValue(new ApiError(404, 'not_found', 'not found'))
    mocked.config.mockResolvedValue({ signup_enabled: false })
    const s = useAuthStore()
    await expect(s.signup('a@x.co', 'Ada', 'long-enough-pw')).rejects.toBeInstanceOf(ApiError)
    expect(s.signupEnabled).toBe(false)
  })
  it('updateProfile replaces the user', async () => {
    mocked.updateProfile.mockResolvedValue({ user: { ...authUser, display_name: 'Ada L' } })
    const s = useAuthStore()
    s.user = authUser
    await s.updateProfile('  Ada L ')
    expect(mocked.updateProfile).toHaveBeenCalledWith('Ada L')
    expect(s.user?.display_name).toBe('Ada L')
  })
  it('changePassword keeps the session', async () => {
    mocked.changePassword.mockResolvedValue(undefined)
    const s = useAuthStore()
    s.user = authUser
    s.status = 'authed'
    await s.changePassword('old', 'new-password-123')
    expect(mocked.changePassword).toHaveBeenCalledWith('old', 'new-password-123')
    expect(s.status).toBe('authed')
    expect(s.user).not.toBeNull()
  })
  it('a wrong current password (422) does not sign out or redirect', async () => {
    mocked.changePassword.mockRejectedValue(new ApiError(422, 'validation_failed', 'x', { current_password: 'Incorrect password' }))
    const s = useAuthStore()
    s.user = authUser
    s.status = 'authed'
    await expect(s.changePassword('bad', 'new-password-123')).rejects.toMatchObject({ status: 422 })
    expect(s.status).toBe('authed')
  })
  it('logout clears stores and goes to /login even if the call fails', async () => {
    mocked.logout.mockRejectedValue(new Error('offline'))
    const router = createAppRouter(createMemoryHistory())
    setRouter(router)
    const s = useAuthStore()
    s.user = authUser
    s.status = 'authed'
    useProjectsStore().list = [{ id: 'p' } as never]
    await s.logout()
    expect(s.status).toBe('anon')
    expect(s.user).toBeNull()
    expect(useProjectsStore().list).toEqual([])
    expect(router.currentRoute.value.name).toBe('login')
  })
})

describe('handleUnauthorized', () => {
  it('clears state, redirects once to /login with the original path, and toasts', async () => {
    const router = createAppRouter(createMemoryHistory())
    setRouter(router)
    mocked.me.mockResolvedValue({ user: authUser, auth: { method: 'session' } })
    await router.push('/p/WEB/t/3?q=x')
    const s = useAuthStore()
    expect(s.status).toBe('authed')
    useBoardStore().selectedRef = 'WEB-3'
    configureClient({ onUnauthorized: () => s.handleUnauthorized(), onToast: () => {}, onForbidden: () => {}, sleep: async () => {} })
    s.handleUnauthorized()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
    expect(router.currentRoute.value.query.redirect).toBe('/p/WEB/t/3?q=x')
    expect(s.status).toBe('anon')
    expect(useBoardStore().selectedRef).toBeNull()
    expect(notify).toHaveBeenCalledWith('error', 'Session expired')
  })
})
