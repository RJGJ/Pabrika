import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import type { Project, Token } from '@/api/types'

vi.mock('@/api/auth', () => ({
  auth: { config: vi.fn(), me: vi.fn(), updateProfile: vi.fn(), changePassword: vi.fn(), logout: vi.fn() },
}))
vi.mock('@/api/tokens', () => ({ tokens: { list: vi.fn(), create: vi.fn(), revoke: vi.fn() } }))
vi.mock('@/api/projects', () => ({ projects: { list: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { auth as authApi } from '@/api/auth'
import { tokens as tokensApi } from '@/api/tokens'
import { projects as projectsApi } from '@/api/projects'
import { notify } from '@/lib/toast'
import { setRouter } from '@/router/instance'
import ChangePasswordForm from '@/components/account/ChangePasswordForm.vue'
import CreateTokenDialog from '@/components/account/CreateTokenDialog.vue'
import McpSnippet from '@/components/account/McpSnippet.vue'
import ProfileForm from '@/components/account/ProfileForm.vue'
import TokenSecretDialog from '@/components/account/TokenSecretDialog.vue'
import TokensTable from '@/components/account/TokensTable.vue'
import { useAuthStore } from '@/stores/auth'
import { useProjectsStore } from '@/stores/projects'
import { useTokensStore } from '@/stores/tokens'

type M = Record<string, ReturnType<typeof vi.fn>>
const aApi = authApi as unknown as M
const tApi = tokensApi as unknown as M
const pApi = projectsApi as unknown as M

const ME = { id: 'u1', email: 'ada@x.co', display_name: 'Ada', created_at: '2026-01-01T00:00:00Z' }
const token = (id: string, extra: Partial<Token> = {}): Token => ({
  id, name: `tok-${id}`, token_prefix: 'pb_abcd', scope: 'read', project: null,
  last_used_at: null, revoked_at: null, created_at: '2026-01-01T00:00:00Z', ...extra,
})
const proj = (key: string): Project => ({
  id: `ULID-${key}`, key, name: key, description: '', archived_at: null, created_at: '', updated_at: '', role: 'owner',
})

const mounted: { unmount(): void }[] = []
const mountIt = (c: object, props: object = {}) => {
  const w = mount(c, { props, attachTo: document.body })
  mounted.push(w)
  return w
}
function typeInto(sel: string, v: string) {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = v
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
function change(sel: string, v: string) {
  const el = document.body.querySelector(sel) as HTMLSelectElement
  el.value = v
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

let router: ReturnType<typeof createRouter>
beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [aApi, tApi, pApi]) for (const fn of Object.values(m)) fn.mockReset()
  vi.mocked(notify).mockReset()
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/login', name: 'login', component: { template: '<div/>' } }] })
  setRouter(router)
  const auth = useAuthStore()
  auth.user = { ...ME }
  auth.status = 'authed'
})

describe('ProfileForm', () => {
  it('shows email read-only and saves the trimmed display name via the store', async () => {
    aApi.updateProfile.mockResolvedValue({ user: { ...ME, display_name: 'Ada L' } })
    const w = mountIt(ProfileForm)
    expect((w.find('#profile-email').element as HTMLInputElement).readOnly).toBe(true)
    await w.find('#profile-name').setValue('  Ada L  ')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(aApi.updateProfile).toHaveBeenCalledWith('Ada L')
    expect(useAuthStore().user?.display_name).toBe('Ada L')
    expect(notify).toHaveBeenCalledWith('success', 'Profile updated')
  })
  it('rejects empty and over-long names client-side', async () => {
    const w = mountIt(ProfileForm)
    await w.find('#profile-name').setValue('   ')
    await w.find('form').trigger('submit')
    expect(w.text()).toContain('Display name must be 1 to 100')
    await w.find('#profile-name').setValue('x'.repeat(101))
    await w.find('form').trigger('submit')
    expect(w.text()).toContain('Display name must be 1 to 100')
    expect(aApi.updateProfile).not.toHaveBeenCalled()
  })
  it('shows server field errors', async () => {
    aApi.updateProfile.mockRejectedValue(new ApiError(422, 'validation_failed', 'bad', { display_name: 'Nope' }))
    const w = mountIt(ProfileForm)
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('#profile-name-error').text()).toBe('Nope')
  })
})

describe('ChangePasswordForm', () => {
  it('hints at least 10 characters and does not call the API', async () => {
    const w = mountIt(ChangePasswordForm)
    await w.find('#current-password').setValue('old-password')
    await w.find('#new-password').setValue('short')
    await w.find('form').trigger('submit')
    expect(w.text()).toContain('at least 10 characters')
    expect(aApi.changePassword).not.toHaveBeenCalled()
  })
  it('wrong current password is a 422 field error and never redirects or signs out', async () => {
    aApi.changePassword.mockRejectedValue(new ApiError(422, 'validation_failed', 'bad', { current_password: 'Incorrect password' }))
    const push = vi.spyOn(router, 'replace')
    const w = mountIt(ChangePasswordForm)
    await w.find('#current-password').setValue('wrong')
    await w.find('#new-password').setValue('a-brand-new-password')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('#current-password-error').text()).toBe('Incorrect password')
    expect(push).not.toHaveBeenCalled()
    expect(useAuthStore().status).toBe('authed')
    expect(useAuthStore().user).not.toBeNull()
  })
  it('success toasts, clears the fields and keeps the session', async () => {
    aApi.changePassword.mockResolvedValue(undefined)
    const w = mountIt(ChangePasswordForm)
    await w.find('#current-password').setValue('old-password')
    await w.find('#new-password').setValue('a-brand-new-password')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(aApi.changePassword).toHaveBeenCalledWith('old-password', 'a-brand-new-password')
    expect((w.find('#current-password').element as HTMLInputElement).value).toBe('')
    expect((w.find('#new-password').element as HTMLInputElement).value).toBe('')
    expect(vi.mocked(notify).mock.calls[0][0]).toBe('success')
    expect(useAuthStore().status).toBe('authed')
  })
  it('shows the rate-limit message on 429', async () => {
    aApi.changePassword.mockRejectedValue(new ApiError(429, 'rate_limited', 'slow down', undefined, 30))
    const w = mountIt(ChangePasswordForm)
    await w.find('#current-password').setValue('old-password')
    await w.find('#new-password').setValue('a-brand-new-password')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('try again in 30 seconds')
  })
})

describe('TokensTable', () => {
  it('mutes revoked rows, drops their Revoke button, and renders limits and last used', () => {
    const s = useTokensStore()
    s.items = [
      token('a', { project: { id: 'ULID-WEB', key: 'WEB' }, scope: 'write', last_used_at: new Date().toISOString() }),
      token('b', { revoked_at: '2026-02-01T00:00:00Z' }),
    ]
    const w = mountIt(TokensTable)
    const a = w.find('[data-testid=token-a]')
    const b = w.find('[data-testid=token-b]')
    expect(a.attributes('data-revoked')).toBe('false')
    expect(a.text()).toContain('WEB')
    expect(a.text()).toContain('write')
    expect(a.find('button[aria-label="Revoke tok-a"]').exists()).toBe(true)
    expect(b.attributes('data-revoked')).toBe('true')
    expect(b.classes().join(' ')).toContain('opacity-60')
    expect(b.text()).toContain('Revoked')
    expect(b.text()).toContain('All projects')
    expect(b.text()).toContain('Never')
    expect(b.find('button').exists()).toBe(false)
  })
  it('revokes after confirm', async () => {
    const s = useTokensStore()
    s.items = [token('a')]
    tApi.revoke.mockResolvedValue(undefined)
    const w = mountIt(TokensTable)
    await w.find('button[aria-label="Revoke tok-a"]').trigger('click')
    await flushPromises()
    expect(tApi.revoke).not.toHaveBeenCalled()
    const d = document.body.querySelector('[role=dialog]')!
    ;([...d.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Revoke') as HTMLButtonElement).click()
    await flushPromises()
    expect(tApi.revoke).toHaveBeenCalledWith('a')
    expect(s.items[0].revoked_at).not.toBeNull()
  })
})

describe('token flow', () => {
  function setupProjects() {
    const p = useProjectsStore()
    p.list = [proj('WEB'), proj('API')]
    p.loaded = true
  }

  it('defaults to read scope and all projects; sends no project_id', async () => {
    setupProjects()
    tApi.create.mockResolvedValue({ token: token('n'), secret: 'pb_secret' })
    const w = mountIt(CreateTokenDialog, { open: true })
    await flushPromises()
    expect((document.body.querySelector('#token-scope') as HTMLSelectElement).value).toBe('read')
    typeInto('#token-name', 'Agent')
    await flushPromises()
    document.body.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(tApi.create).toHaveBeenCalledWith({ name: 'Agent', scope: 'read' })
    w.unmount()
    mounted.pop()
  })

  it('project limit sends the project ULID, not the key', async () => {
    setupProjects()
    tApi.create.mockResolvedValue({ token: token('n', { scope: 'write' }), secret: 'pb_secret' })
    mountIt(CreateTokenDialog, { open: true })
    await flushPromises()
    typeInto('#token-name', 'Scoped')
    change('#token-scope', 'write')
    change('#token-project', 'ULID-API')
    await flushPromises()
    document.body.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(tApi.create).toHaveBeenCalledWith({ name: 'Scoped', scope: 'write', project_id: 'ULID-API' })
  })

  it('validates the name and shows 422 errors', async () => {
    setupProjects()
    mountIt(CreateTokenDialog, { open: true })
    await flushPromises()
    document.body.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(document.body.textContent).toContain('Name must be 1 to 100')
    expect(tApi.create).not.toHaveBeenCalled()
    tApi.create.mockRejectedValue(new ApiError(422, 'validation_failed', 'Too many active tokens'))
    typeInto('#token-name', 'x')
    await flushPromises()
    document.body.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(document.body.textContent).toContain('Too many active tokens')
  })

  it('secret is shown once and cleared from memory when the dialog closes', async () => {
    setupProjects()
    tApi.create.mockResolvedValue({ token: token('n', { name: 'Agent' }), secret: 'pb_supersecret' })
    const s = useTokensStore()
    mountIt(TokenSecretDialog)
    expect(document.body.querySelector('[data-testid=token-secret-dialog]')).toBeNull()
    await s.create({ name: 'Agent', scope: 'read' })
    await flushPromises()
    const input = document.body.querySelector('[data-testid=token-secret]') as HTMLInputElement
    expect(input.value).toBe('pb_supersecret')
    expect(document.body.textContent).toContain("You won't be able to see this again")
    ;(document.body.querySelector('[data-testid=close-secret]') as HTMLButtonElement).click()
    await flushPromises()
    expect(s.secret).toBeNull()
    expect(s.snippetSecret).toBeNull()
    expect(document.body.querySelector('[data-testid=token-secret]')).toBeNull()
    expect(JSON.stringify(localStorage) + JSON.stringify(sessionStorage)).not.toContain('pb_supersecret')
  })

  it('copy writes the secret to the clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    tApi.create.mockResolvedValue({ token: token('n'), secret: 'pb_copyme' })
    const s = useTokensStore()
    mountIt(TokenSecretDialog)
    await s.create({ name: 'n', scope: 'read' })
    await flushPromises()
    ;(document.body.querySelector('[data-testid=copy-secret]') as HTMLButtonElement).click()
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('pb_copyme')
  })

  it('"Use in MCP snippet" fills the snippet in memory only and clears the dialog secret', async () => {
    tApi.create.mockResolvedValue({ token: token('n'), secret: 'pb_fillme' })
    const s = useTokensStore()
    mountIt(TokenSecretDialog)
    const snippet = mountIt(McpSnippet)
    expect(snippet.get('[data-testid=mcp-command]').text()).toContain('Bearer pb_your_token_here')
    await s.create({ name: 'n', scope: 'read' })
    await flushPromises()
    ;(document.body.querySelector('[data-testid=use-in-snippet]') as HTMLButtonElement).click()
    await flushPromises()
    expect(s.secret).toBeNull()
    expect(snippet.get('[data-testid=mcp-command]').text()).toContain('Bearer pb_fillme')
    expect(JSON.stringify(localStorage)).not.toContain('pb_fillme')
    s.reset() // leaving the account screen
    await flushPromises()
    expect(snippet.get('[data-testid=mcp-command]').text()).toContain('pb_your_token_here')
  })
})

describe('McpSnippet', () => {
  it('uses window.location.origin and mentions using the Go server address in dev', () => {
    const w = mountIt(McpSnippet)
    expect(w.get('[data-testid=mcp-command]').text()).toBe(
      `claude mcp add --transport http pabrika ${window.location.origin}/mcp --header "Authorization: Bearer pb_your_token_here"`,
    )
    expect(w.text()).toContain("Go server's address")
    expect(w.text().toLowerCase()).not.toContain('edit')
  })
})

describe('tokens store', () => {
  it('create puts the new token first and keeps the secret only in memory', async () => {
    tApi.create.mockResolvedValue({ token: token('new'), secret: 'pb_x' })
    const s = useTokensStore()
    s.items = [token('old')]
    await s.create({ name: 'n', scope: 'read' })
    expect(s.items.map((t) => t.id)).toEqual(['new', 'old'])
    expect(s.secret?.value).toBe('pb_x')
    s.clearSecret()
    expect(s.secret).toBeNull()
  })
  it('load surfaces errors', async () => {
    tApi.list.mockRejectedValue(new ApiError(500, 'internal', 'boom'))
    const s = useTokensStore()
    await s.load()
    expect(s.error).toBe('boom')
  })
})
